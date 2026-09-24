'use client';
import { useState } from 'react';
import { C } from '../constants';
import type { AppFlowDebugSessionState } from '../hooks/useAppFlowDebugSession';
import type { AppFlowRuntimeParamSpec, AppFlowLLMCredentialValue } from '../types';
import type { AppFlowDebugPreset } from '@/lib/api';
import { AppFlowLLMCredentialField } from './AppFlowLLMCredentialField';
import { AppFlowDebugLogView } from './AppFlowDebugLogView';

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

// DEBUG_RUN_MAX_LIFETIME_LABEL mirrors go/internal/appflow's
// DebugRunMaxLifetime (3h30m) — shown before a run starts so the bound is
// visible up front, not just discovered after the fact via expiresAt.
const DEBUG_RUN_MAX_LIFETIME_LABEL = '3h 30m';

function formatExpiry(iso: string): string {
  const ms = new Date(iso).getTime() - Date.now();
  if (Number.isNaN(ms)) return iso;
  if (ms <= 0) return 'now';
  const mins = Math.round(ms / 60000);
  if (mins < 60) return `in ${mins}m`;
  return `in ${Math.floor(mins / 60)}h ${mins % 60}m`;
}

export function AppFlowDebugPanel({
  appId,
  debug,
  entryPointOptions,
  runtimeParamSpecs,
  onSetEntryPointSlug,
  onSetUserMessage,
  onSetCredential,
  onSetStepMode,
  onRunAll,
  onStep,
  onReset,
  onClose,
  presets,
  presetError,
  onSavePreset,
  onLoadPreset,
  onDeletePreset,
}: {
  appId: string;
  debug: AppFlowDebugSessionState;
  entryPointOptions: string[];
  runtimeParamSpecs: AppFlowRuntimeParamSpec[];
  onSetEntryPointSlug: (slug: string) => void;
  onSetUserMessage: (msg: string) => void;
  onSetCredential: (specKey: string, value: AppFlowLLMCredentialValue) => void;
  onSetStepMode: (stepMode: boolean) => void;
  onRunAll: () => void;
  onStep: () => void;
  onReset: () => void;
  onClose: () => void;
  presets: AppFlowDebugPreset[];
  presetError: string | null;
  onSavePreset: (name: string) => void;
  onLoadPreset: (presetId: string) => void;
  onDeletePreset: (presetId: string) => void;
}) {
  const credentialSpecs = runtimeParamSpecs.filter(s => s.type === 'llm_credential');
  const [selectedPresetId, setSelectedPresetId] = useState('');
  const [savingName, setSavingName] = useState('');
  // At least one node is genuinely paused, waiting for the next Step click —
  // derived from nodeStates rather than a separate tracked field, since
  // "paused" is already an observed backend state (docs/APP_CANVAS_DEBUG_PLAN.md
  // Phase 6), not something the UI needs to infer independently.
  const awaitingStep = Object.values(debug.nodeStates).some(s => s === 'paused');
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

        <label style={{ display: 'flex', alignItems: 'center', gap: 5, fontSize: '11px', color: C.textMuted, cursor: debug.running ? 'not-allowed' : 'pointer', alignSelf: 'center' }}>
          <input
            type="checkbox"
            checked={debug.stepMode}
            disabled={debug.running}
            onChange={e => onSetStepMode(e.target.checked)}
          />
          Step mode
        </label>

        <div style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
          <label style={{ fontSize: '10px', color: C.textMuted, fontWeight: 700, letterSpacing: '0.06em' }}>PRESET</label>
          <div style={{ display: 'flex', gap: 4 }}>
            <select
              value={selectedPresetId}
              disabled={debug.running}
              onChange={e => {
                setSelectedPresetId(e.target.value);
                if (e.target.value) onLoadPreset(e.target.value);
              }}
              style={{ ...inputStyle, width: '140px', fontSize: '12px' }}
            >
              <option value="">— load preset —</option>
              {presets.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
            </select>
            {selectedPresetId && (
              <button
                onClick={() => { onDeletePreset(selectedPresetId); setSelectedPresetId(''); }}
                disabled={debug.running}
                title="Delete this preset"
                style={{ background: 'transparent', border: `1px solid ${C.outline}`, color: '#f87171', borderRadius: 6, cursor: debug.running ? 'not-allowed' : 'pointer', fontSize: '12px', padding: '0 8px' }}
              >✕</button>
            )}
          </div>
        </div>

        <div style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
          <label style={{ fontSize: '10px', color: C.textMuted, fontWeight: 700, letterSpacing: '0.06em' }}>SAVE AS</label>
          <div style={{ display: 'flex', gap: 4 }}>
            <input
              value={savingName}
              onChange={e => setSavingName(e.target.value)}
              disabled={debug.running}
              placeholder="Preset name…"
              style={{ ...inputStyle, width: '140px', fontSize: '12px' }}
            />
            <button
              onClick={() => { if (savingName.trim()) { onSavePreset(savingName.trim()); setSavingName(''); } }}
              disabled={debug.running || !savingName.trim()}
              title="Save current entry point, message, step mode, and LLM credentials as a preset"
              style={{
                background: 'rgba(96,165,250,0.12)', border: '1px solid rgba(96,165,250,0.5)', color: '#60a5fa',
                borderRadius: 6, cursor: debug.running || !savingName.trim() ? 'not-allowed' : 'pointer', fontSize: '12px', padding: '0 10px',
                opacity: debug.running || !savingName.trim() ? 0.6 : 1,
              }}
            >💾 Save</button>
          </div>
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
          {debug.stepMode ? '▶ Start' : '▶ Run All'}
        </button>

        {debug.stepMode && debug.runId && (
          <button
            onClick={onStep}
            disabled={!awaitingStep || debug.done}
            title={awaitingStep ? 'Advance every currently-paused node by one tick' : 'Waiting for the run to reach its next pause point…'}
            style={{
              background: 'rgba(192,132,252,0.12)', border: '1px solid rgba(192,132,252,0.5)',
              color: '#c084fc', padding: '6px 16px', borderRadius: '6px',
              cursor: !awaitingStep || debug.done ? 'not-allowed' : 'pointer',
              fontSize: '12px', fontWeight: 700, opacity: !awaitingStep || debug.done ? 0.5 : 1,
            }}
          >
            ⏭ Step
          </button>
        )}

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
        {debug.workflowId && (
          <a
            href={'/temporal/namespaces/default/workflows?query=' + encodeURIComponent(`WorkflowId="${debug.workflowId}"`)}
            target="_blank"
            rel="noopener noreferrer"
            title={`Open in Temporal UI: ${debug.workflowId}`}
            style={{ fontSize: '11px', color: '#5b7fff', fontFamily: 'monospace', textDecoration: 'none', display: 'flex', alignItems: 'center', gap: '3px' }}
          >
            <span className="material-symbols-outlined" style={{ fontSize: '13px' }}>open_in_new</span>
            View in Temporal
          </a>
        )}
        {debug.expiresAt && (
          <span style={{ color: '#f59e0b', fontSize: '11px' }} title={new Date(debug.expiresAt).toLocaleString()}>
            ⏱ expires {formatExpiry(debug.expiresAt)}
          </span>
        )}
        {debug.error && (
          <span style={{ color: '#f87171', fontSize: '11px', maxWidth: '320px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            ✗ {debug.error}
          </span>
        )}
        {presetError && (
          <span style={{ color: '#f87171', fontSize: '11px', maxWidth: '320px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            ✗ preset: {presetError}
          </span>
        )}
      </div>
      <div style={{ marginTop: 6, color: '#475569', fontSize: '10px' }}>
        Runs the saved draft directly on the isolated debug worker pool — no publish required.
        Watch nodes light up on the canvas as they really execute. Debug runs are bounded to a
        maximum of {DEBUG_RUN_MAX_LIFETIME_LABEL} (covers node retries and worker queue waits) —
        the run is terminated and any per-node credentials are cleared after that, or immediately
        once the run finishes.
      </div>

      {!debug.error && debug.done && (
        // A prominent completion banner, not just small toolbar text — added
        // after a live walkthrough where a fast run (an echo agent completing
        // in single-digit milliseconds) finished with no clear signal to the
        // user that the flow had actually reached its end.
        <div style={{
          marginTop: 10, padding: '8px 12px', borderRadius: 8,
          background: 'rgba(74,222,128,0.12)', border: '1px solid rgba(74,222,128,0.4)',
          display: 'flex', alignItems: 'center', gap: 8,
        }}>
          <span className="material-symbols-outlined" style={{ fontSize: 18, color: '#4ade80' }}>check_circle</span>
          <span style={{ color: '#4ade80', fontSize: '13px', fontWeight: 700 }}>Debug run complete</span>
          <span style={{ color: '#94a3b8', fontSize: '11px' }}>— every reached node finished. Click a node to inspect its output.</span>
        </div>
      )}

      {debug.runId && (debug.done || debug.error) && (
        <AppFlowDebugLogView appId={appId} runId={debug.runId} />
      )}

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
