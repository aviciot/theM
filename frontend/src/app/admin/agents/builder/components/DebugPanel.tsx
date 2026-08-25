import type { DebugState, DebugParamSpec } from '../types';
import { C, inputStyle } from '../constants';

interface DebugPanelProps {
  debug: DebugState;
  setDebug: React.Dispatch<React.SetStateAction<DebugState>>;
  debugRunning: boolean;
  debugCommitSetup: () => void;
  debugRunAll: () => void;
  debugStep: () => void;
  debugReset: () => void;
}

export function DebugPanel({
  debug,
  setDebug,
  debugRunning,
  debugCommitSetup,
  debugRunAll,
  debugStep,
  debugReset,
}: DebugPanelProps) {
  return (
    <div style={{
      flexShrink: 0, borderBottom: `1px solid ${C.amberBorder}`,
      background: 'rgba(245,158,11,0.06)', padding: '10px 16px',
    }}>
      {!debug.setupComplete && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <span style={{ color: C.amber, fontSize: '11px', fontWeight: 700, letterSpacing: '0.08em' }}>
              DEBUG SETUP
            </span>
            <span style={{ color: '#64748b', fontSize: '11px' }}>
              Fill in the values the pipeline needs, then click Start
            </span>
          </div>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10, alignItems: 'flex-end' }}>
            {debug.paramSpecs.map((spec: DebugParamSpec) => (
              <div key={spec.key} style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
                <label style={{ fontSize: '10px', color: spec.isSecret ? C.amber : C.textMuted, fontWeight: 700, letterSpacing: '0.06em' }}>
                  {spec.label.toUpperCase()}
                  {spec.isSecret && <span style={{ marginLeft: 4, color: '#f59e0b', fontSize: '9px' }}>🔒 session only</span>}
                </label>
                {spec.key === '__test_input' ? (
                  <input
                    value={debug.debugParams[spec.key] ?? ''}
                    onChange={e => setDebug(prev => ({ ...prev, debugParams: { ...prev.debugParams, [spec.key]: e.target.value } }))}
                    placeholder="Compare Rome and Barcelona for a kosher trip…"
                    style={{ ...inputStyle, width: '300px', fontSize: '12px' }}
                  />
                ) : spec.options ? (
                  <select
                    value={debug.debugParams[spec.key] ?? ''}
                    onChange={e => setDebug(prev => ({ ...prev, debugParams: { ...prev.debugParams, [spec.key]: e.target.value } }))}
                    title={spec.description}
                    style={{ ...inputStyle, width: '160px', fontSize: '12px' }}
                  >
                    <option value="">— {spec.label} —</option>
                    {spec.options.map(opt => <option key={opt} value={opt}>{opt}</option>)}
                  </select>
                ) : (
                  <input
                    value={debug.debugParams[spec.key] ?? ''}
                    onChange={e => setDebug(prev => ({ ...prev, debugParams: { ...prev.debugParams, [spec.key]: e.target.value } }))}
                    placeholder={spec.isSecret ? '••••••••' : spec.label}
                    type={spec.isSecret ? 'password' : 'text'}
                    title={spec.description}
                    style={{ ...inputStyle, width: '200px', fontSize: '12px' }}
                  />
                )}
              </div>
            ))}
            <button onClick={debugCommitSetup} style={{
              background: 'rgba(74,222,128,0.12)', border: '1px solid rgba(74,222,128,0.5)',
              color: '#4ade80', padding: '6px 16px', borderRadius: '6px', cursor: 'pointer', fontSize: '12px', fontWeight: 700, alignSelf: 'flex-end',
            }}>
              ▶ Start Debug
            </button>
          </div>
          {debug.error && (
            <span style={{ color: '#f87171', fontSize: '11px' }}>✗ {debug.error}</span>
          )}
          <span style={{ color: '#475569', fontSize: '10px' }}>
            🔒 API keys and secrets are stored in your browser session only — never sent to the-M server. Test messages are saved to your profile.
          </span>
        </div>
      )}

      {debug.setupComplete && (
        <div style={{ display: 'flex', alignItems: 'center', gap: '10px', flexWrap: 'wrap' }}>
          <span style={{ color: C.amber, fontSize: '11px', fontWeight: 700, letterSpacing: '0.08em', flexShrink: 0, display: 'flex', alignItems: 'center', gap: 5 }}>
            DEBUG
            {debugRunning && (
              <>
                <style>{`@keyframes dbg-dot{0%,80%,100%{opacity:0.15}40%{opacity:1}}`}</style>
                {[0, 0.22, 0.44].map(delay => (
                  <span key={delay} style={{ width: 5, height: 5, borderRadius: '50%', background: C.amber, display: 'inline-block', animation: `dbg-dot 1.1s ease-in-out ${delay}s infinite` }} />
                ))}
              </>
            )}
          </span>
          <span style={{ color: '#64748b', fontSize: '11px', maxWidth: '220px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {debug.debugParams['__test_input']}
          </span>
          <button onClick={() => setDebug(prev => ({ ...prev, setupComplete: false }))} style={{
            background: 'transparent', border: `1px solid ${C.outline}`, color: C.textMuted,
            padding: '4px 10px', borderRadius: '6px', cursor: 'pointer', fontSize: '11px',
          }}>Edit inputs</button>
          <button onClick={debugRunAll} disabled={debug.mode === 'run-all' && debug.currentStepIndex < debug.executionOrder.length && debug.currentStepIndex > 0} style={{
            background: 'rgba(74,222,128,0.1)', border: '1px solid rgba(74,222,128,0.4)',
            color: '#4ade80', padding: '5px 12px', borderRadius: '6px', cursor: 'pointer', fontSize: '12px', fontWeight: 600,
          }}>▶ Run All</button>
          <button onClick={debugStep} style={{
            background: 'rgba(96,165,250,0.1)', border: '1px solid rgba(96,165,250,0.4)',
            color: '#60a5fa', padding: '5px 12px', borderRadius: '6px', cursor: 'pointer', fontSize: '12px', fontWeight: 600,
          }}>⏭ Step</button>
          <button onClick={debugReset} style={{
            background: 'transparent', border: `1px solid ${C.outline}`,
            color: C.textMuted, padding: '5px 12px', borderRadius: '6px', cursor: 'pointer', fontSize: '12px',
          }}>⏹ Reset</button>
          {debug.error && (
            <span style={{ color: '#f87171', fontSize: '11px', maxWidth: '260px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
              ✗ {debug.error}
            </span>
          )}
          {!debug.error && debug.mode === 'run-all' && debug.currentStepIndex > 0 && debug.currentStepIndex >= debug.executionOrder.length && (
            <span style={{ color: '#4ade80', fontSize: '11px' }}>✓ Complete — {debug.executionOrder.length} steps</span>
          )}
          {!debug.error && debug.mode === 'step' && debug.currentStepIndex > 0 && (
            <span style={{ color: '#60a5fa', fontSize: '11px' }}>
              Step {Math.min(debug.currentStepIndex, debug.executionOrder.length)}/{debug.executionOrder.length}
            </span>
          )}
        </div>
      )}
    </div>
  );
}
