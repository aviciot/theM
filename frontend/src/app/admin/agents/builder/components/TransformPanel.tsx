'use client';
import { useState, useEffect, useCallback } from 'react';
import { C, inputStyle, labelStyle } from '../constants';
import { api } from '@/lib/api';

// ── Types ─────────────────────────────────────────────────────────────────────

interface ArgDef {
  key: string;
  description: string;
  required: boolean;
  default?: string;
}

interface FunctionDef {
  name: string;
  category: string;
  description: string;
  args: ArgDef[];
  examples: Array<{ in: string; args?: Record<string, string>; out: string }>;
}

interface FunctionStep {
  fn: string;
  input_var: string;
  output_var: string;
  args?: Record<string, string>;
}

interface StepResult {
  fn: string;
  input_var: string;
  output_var: string;
  in: string;
  out?: string;
  error?: string;
  ok: boolean;
  duration_ns: number;
}

interface TransformPanelProps {
  cfg: Record<string, unknown>;
  updateStepConfig: (key: string, value: unknown) => void;
  availableVars: string[];
}

// ── Catalog fetch (cached per session) ───────────────────────────────────────

let catalogCache: { functions: FunctionDef[]; by_category: Record<string, FunctionDef[]> } | null = null;

async function fetchCatalog() {
  if (catalogCache) return catalogCache;
  catalogCache = await api.get<{ functions: FunctionDef[]; by_category: Record<string, FunctionDef[]> }>('/admin/transform-functions');
  return catalogCache!;
}

// ── Styles ────────────────────────────────────────────────────────────────────

const sectionTitle = (color: string): React.CSSProperties => ({
  fontSize: '10px', fontWeight: 700, color, letterSpacing: '0.08em',
  marginBottom: '8px', marginTop: '14px',
});

// Selects need explicit dark background so option text is readable on all OSes/browsers.
const selectStyle: React.CSSProperties = {
  ...inputStyle,
  background: '#1e293b',
  color: '#f1f5f9',
};

const stepCard = (ok: boolean | null): React.CSSProperties => ({
  background: ok === null ? 'rgba(99,102,241,0.06)' : ok ? 'rgba(74,222,128,0.06)' : 'rgba(248,113,113,0.06)',
  border: `1px solid ${ok === null ? 'rgba(99,102,241,0.25)' : ok ? 'rgba(74,222,128,0.25)' : 'rgba(248,113,113,0.35)'}`,
  borderRadius: '6px', padding: '8px 10px', marginBottom: 6,
});

const monoStyle: React.CSSProperties = {
  fontFamily: 'JetBrains Mono, monospace', fontSize: '11px',
};

// ── FunctionRow ───────────────────────────────────────────────────────────────

function FunctionRow({
  step, index, catalog, availableVars, result, onChange, onRemove, onMoveUp, onMoveDown,
}: {
  step: FunctionStep;
  index: number;
  catalog: { functions: FunctionDef[]; by_category: Record<string, FunctionDef[]> } | null;
  availableVars: string[];
  result?: StepResult;
  onChange: (step: FunctionStep) => void;
  onRemove: () => void;
  onMoveUp: () => void;
  onMoveDown: () => void;
}) {
  const def = catalog?.functions.find(f => f.name === step.fn);
  const ok = result ? result.ok : null;

  return (
    <div style={stepCard(ok)}>
      {/* Header: index, fn picker, move, remove — and result badge */}
      <div style={{ display: 'flex', gap: 4, alignItems: 'center', marginBottom: 6 }}>
        <span style={{ fontSize: '10px', color: C.textMuted, minWidth: 18 }}>{index + 1}.</span>
        <select
          value={step.fn}
          onChange={e => onChange({ ...step, fn: e.target.value, args: {} })}
          style={{ ...selectStyle, flex: 1, fontSize: '11px' }}
        >
          <option value="">— pick function —</option>
          {catalog && Object.entries(catalog.by_category).map(([cat, fns]) => (
            <optgroup key={cat} label={cat.toUpperCase()}>
              {fns.map(f => (
                <option key={f.name} value={f.name}>{f.name}</option>
              ))}
            </optgroup>
          ))}
        </select>
        {result && (
          <span style={{ fontSize: '10px', fontWeight: 700, color: result.ok ? '#4ade80' : '#f87171', whiteSpace: 'nowrap' }}>
            {result.ok ? '✓' : '✗'} {(result.duration_ns / 1_000_000).toFixed(2)}ms
          </span>
        )}
        <button onClick={onMoveUp} title="Move up" style={{ background: 'transparent', border: 'none', color: C.textMuted, cursor: 'pointer', fontSize: '12px', padding: '0 2px' }}>↑</button>
        <button onClick={onMoveDown} title="Move down" style={{ background: 'transparent', border: 'none', color: C.textMuted, cursor: 'pointer', fontSize: '12px', padding: '0 2px' }}>↓</button>
        <button onClick={onRemove} title="Remove" style={{ background: 'transparent', border: 'none', color: '#f87171', cursor: 'pointer', fontSize: '14px', padding: '0 2px' }}>×</button>
      </div>

      {/* fn description */}
      {def && (
        <div style={{ fontSize: '10px', color: '#475569', marginBottom: 6, fontStyle: 'italic' }}>
          {def.description}
        </div>
      )}

      {/* in: */}
      <div style={{ display: 'flex', gap: 4, alignItems: 'center', marginBottom: 4 }}>
        <span style={{ fontSize: '10px', color: C.textMuted, minWidth: 42 }}>in:</span>
        <select
          value={step.input_var}
          onChange={e => onChange({ ...step, input_var: e.target.value })}
          style={{ ...selectStyle, flex: 1, fontSize: '11px', ...monoStyle }}
        >
          <option value="">— pick var —</option>
          {[...new Set(availableVars)].map(v => <option key={v} value={v}>{v}</option>)}
        </select>
      </div>

      {/* args — between in and out */}
      {def?.args.map(arg => (
        <div key={arg.key} style={{ display: 'flex', gap: 4, alignItems: 'center', marginBottom: 4 }}>
          <span style={{ fontSize: '10px', color: C.textMuted, minWidth: 42 }} title={arg.description}>
            {arg.key}:{arg.required && <span style={{ color: '#f87171' }}>*</span>}
          </span>
          <input
            value={step.args?.[arg.key] ?? ''}
            onChange={e => onChange({ ...step, args: { ...step.args, [arg.key]: e.target.value } })}
            style={{ ...inputStyle, flex: 1, fontSize: '11px', ...monoStyle }}
            placeholder={arg.default ?? arg.description}
          />
        </div>
      ))}

      {/* out: */}
      <div style={{ display: 'flex', gap: 4, alignItems: 'center', marginBottom: 4 }}>
        <span style={{ fontSize: '10px', color: C.textMuted, minWidth: 42 }}>out:</span>
        <input
          value={step.output_var}
          onChange={e => onChange({ ...step, output_var: e.target.value })}
          style={{ ...inputStyle, flex: 1, fontSize: '11px', ...monoStyle }}
          placeholder="output_var_name"
        />
      </div>

      {/* Inline result output */}
      {result && result.ok && result.out !== undefined && (
        <div style={{ marginTop: 6, borderTop: '1px solid rgba(74,222,128,0.2)', paddingTop: 4 }}>
          <div style={{ fontSize: '9px', color: '#4ade80', marginBottom: 2 }}>→ {result.output_var}</div>
          <pre style={{ margin: 0, fontSize: '10px', color: '#4ade80', whiteSpace: 'pre-wrap', wordBreak: 'break-all', maxHeight: 80, overflow: 'auto' }}>
            {result.out.length > 200 ? result.out.slice(0, 200) + '…' : result.out}
          </pre>
        </div>
      )}
      {result && !result.ok && result.error && (
        <div style={{ marginTop: 6, borderTop: '1px solid rgba(248,113,113,0.2)', paddingTop: 4 }}>
          <div style={{ fontSize: '10px', color: '#f87171' }}>✗ {result.error}</div>
        </div>
      )}
    </div>
  );
}

// ── TransformPanel ────────────────────────────────────────────────────────────

export function TransformPanel({ cfg, updateStepConfig, availableVars }: TransformPanelProps) {
  const [catalog, setCatalog] = useState<{ functions: FunctionDef[]; by_category: Record<string, FunctionDef[]> } | null>(null);
  const [catalogError, setCatalogError] = useState('');
  const [testVars, setTestVars] = useState<Record<string, string>>({});
  const [testResults, setTestResults] = useState<StepResult[] | null>(null);
  const [testRunning, setTestRunning] = useState(false);
  const [testError, setTestError] = useState('');
  const [aiOpen, setAiOpen] = useState(false);

  const functions: FunctionStep[] = (cfg.functions as FunctionStep[]) ?? [];

  useEffect(() => {
    fetchCatalog().then(setCatalog).catch(e => setCatalogError(e.message));
  }, []);

  // Input vars for test: only vars the chain actually reads from outside (not produced by earlier steps).
  const chainOutputs = new Set(functions.map(s => s.output_var).filter(Boolean));
  const testInputVars = Array.from(new Set(
    functions.map(s => s.input_var).filter(v => v && !chainOutputs.has(v))
  ));

  const updateFunctions = useCallback((next: FunctionStep[]) => {
    updateStepConfig('functions', next);
  }, [updateStepConfig]);

  const runTest = async () => {
    setTestRunning(true);
    setTestResults(null);
    setTestError('');
    try {
      const vars: Record<string, unknown> = {};
      for (const [k, v] of Object.entries(testVars)) {
        if (k) vars[k] = v;
      }
      const resp = await fetch('/api/them/admin/transform-test', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ functions, vars }),
      });
      const text = await resp.text();
      let data: { steps?: StepResult[] } = {};
      try { data = JSON.parse(text); } catch { /* not JSON */ }
      if (data.steps) {
        setTestResults(data.steps);
        if (!resp.ok && !data.steps.some(s => !s.ok)) {
          setTestError(`Server error ${resp.status}: ${text.trim()}`);
        }
      } else if (!resp.ok) {
        setTestError(`Error ${resp.status}: ${text.trim()}`);
      }
    } catch (e: unknown) {
      setTestError(e instanceof Error ? e.message : String(e));
    } finally {
      setTestRunning(false);
    }
  };

  const resultByIndex = (i: number): StepResult | undefined => testResults?.[i];

  return (
    <>
      <div style={{ color: C.indigo, fontSize: '10px', fontWeight: 700, letterSpacing: '0.08em', marginBottom: '10px' }}>
        TRANSFORM CONFIG
      </div>

      {/* ── Input vars with test value fields ── */}
      {testInputVars.length > 0 && (
        <>
          <div style={sectionTitle(C.cyan)}>INPUT VARS — test values</div>
          {testInputVars.map(varName => (
            <div key={varName} style={{ marginBottom: 8 }}>
              <label style={{ ...labelStyle, marginBottom: 2 }}>
                <code style={{ color: C.cyan, fontSize: '11px' }}>{varName}</code>
              </label>
              <textarea
                value={testVars[varName] ?? ''}
                onChange={e => setTestVars(prev => ({ ...prev, [varName]: e.target.value }))}
                style={{ width: '100%', minHeight: 48, background: '#0f172a', border: `1px solid ${C.outline}`, borderRadius: 4, color: C.text, padding: '6px 8px', fontSize: '11px', fontFamily: 'JetBrains Mono, monospace', resize: 'vertical', boxSizing: 'border-box' }}
                placeholder={`Paste test value for ${varName}…`}
              />
            </div>
          ))}
        </>
      )}

      {/* ── Function chain ── */}
      <div style={sectionTitle(C.indigo)}>FUNCTION CHAIN</div>
      {catalogError && <div style={{ fontSize: '11px', color: '#f87171', marginBottom: 8 }}>Could not load function catalog: {catalogError}</div>}
      {functions.map((step, i) => (
        <FunctionRow
          key={i}
          step={step}
          index={i}
          catalog={catalog}
          availableVars={[...new Set([...availableVars, ...functions.slice(0, i).map(s => s.output_var).filter(Boolean)])]}
          result={resultByIndex(i)}
          onChange={s => { const next = functions.map((x, j) => j === i ? s : x); updateFunctions(next); }}
          onRemove={() => { updateFunctions(functions.filter((_, j) => j !== i)); setTestResults(null); }}
          onMoveUp={() => { const next = [...functions]; if (i > 0) { [next[i-1], next[i]] = [next[i], next[i-1]]; updateFunctions(next); } }}
          onMoveDown={() => { const next = [...functions]; if (i < next.length-1) { [next[i], next[i+1]] = [next[i+1], next[i]]; updateFunctions(next); } }}
        />
      ))}
      <button onClick={() => updateFunctions([...functions, { fn: '', input_var: '', output_var: '', args: {} }])}
        style={{ background: 'transparent', border: `1px dashed ${C.outline}`, color: C.textMuted, padding: '6px 10px', borderRadius: '4px', cursor: 'pointer', fontSize: '11px', width: '100%', marginBottom: 10 }}>
        + Add function step
      </button>

      {/* ── Run Test button ── */}
      {testError && <div style={{ fontSize: '11px', color: '#f87171', marginBottom: 6 }}>{testError}</div>}
      <button
        onClick={runTest}
        disabled={testRunning || functions.length === 0}
        style={{ width: '100%', padding: '8px', borderRadius: 6, border: 'none', background: functions.length === 0 ? '#334155' : C.indigo, color: '#fff', fontWeight: 700, fontSize: '12px', cursor: functions.length === 0 ? 'not-allowed' : 'pointer', marginBottom: 14 }}
      >
        {testRunning ? 'Running…' : '▶ Run Test'}
      </button>
      {functions.length === 0 && <div style={{ fontSize: '11px', color: C.textMuted, textAlign: 'center', marginBottom: 10 }}>Add function steps above first.</div>}

      {/* ── AI Assist (stub, collapsed) ── */}
      <details open={aiOpen} onToggle={e => setAiOpen((e.currentTarget as HTMLDetailsElement).open)} style={{ marginBottom: 8 }}>
        <summary style={{ ...sectionTitle('#64748b'), cursor: 'pointer', listStyle: 'none', marginTop: 4 }}>
          ▸ AI TRANSFORM ASSISTANT
        </summary>
        <div style={{ textAlign: 'center', padding: '20px 12px', border: `1px dashed ${C.outline}`, borderRadius: 8, marginTop: 8 }}>
          <div style={{ fontSize: '22px', marginBottom: 6 }}>🤖</div>
          <div style={{ fontSize: '12px', fontWeight: 700, color: C.text, marginBottom: 4 }}>AI Transform Assistant</div>
          <div style={{ fontSize: '11px', color: C.textMuted, lineHeight: 1.6 }}>
            Describe what you want to do and the assistant will suggest a function chain.<br />
            <span style={{ color: '#475569' }}>Coming soon.</span>
          </div>
        </div>
      </details>
    </>
  );
}
