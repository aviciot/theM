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

const tabBtn = (active: boolean): React.CSSProperties => ({
  flex: 1, padding: '6px 0', fontSize: '11px', fontWeight: 700,
  background: active ? C.indigo : 'transparent',
  color: active ? '#fff' : C.textMuted,
  border: `1px solid ${active ? C.indigo : C.outline}`,
  borderRadius: '4px', cursor: 'pointer',
});

const rowStyle: React.CSSProperties = {
  display: 'flex', gap: 4, marginBottom: 6, alignItems: 'flex-start',
};

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
  step, index, catalog, availableVars, onChange, onRemove, onMoveUp, onMoveDown,
}: {
  step: FunctionStep;
  index: number;
  catalog: { functions: FunctionDef[]; by_category: Record<string, FunctionDef[]> } | null;
  availableVars: string[];
  onChange: (step: FunctionStep) => void;
  onRemove: () => void;
  onMoveUp: () => void;
  onMoveDown: () => void;
}) {
  const def = catalog?.functions.find(f => f.name === step.fn);

  return (
    <div style={stepCard(null)}>
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
        <button onClick={onMoveUp} title="Move up" style={{ background: 'transparent', border: 'none', color: C.textMuted, cursor: 'pointer', fontSize: '12px', padding: '0 2px' }}>↑</button>
        <button onClick={onMoveDown} title="Move down" style={{ background: 'transparent', border: 'none', color: C.textMuted, cursor: 'pointer', fontSize: '12px', padding: '0 2px' }}>↓</button>
        <button onClick={onRemove} title="Remove" style={{ background: 'transparent', border: 'none', color: '#f87171', cursor: 'pointer', fontSize: '14px', padding: '0 2px' }}>×</button>
      </div>

      <div style={{ display: 'flex', gap: 4, alignItems: 'center', marginBottom: 4 }}>
        <span style={{ fontSize: '10px', color: C.textMuted, minWidth: 42 }}>in:</span>
        <select
          value={step.input_var}
          onChange={e => onChange({ ...step, input_var: e.target.value })}
          style={{ ...selectStyle, flex: 1, fontSize: '11px', ...monoStyle }}
        >
          <option value="">— pick var —</option>
          {availableVars.map(v => <option key={v} value={v}>{v}</option>)}
        </select>
      </div>

      <div style={{ display: 'flex', gap: 4, alignItems: 'center', marginBottom: 4 }}>
        <span style={{ fontSize: '10px', color: C.textMuted, minWidth: 42 }}>out:</span>
        <input
          value={step.output_var}
          onChange={e => onChange({ ...step, output_var: e.target.value })}
          style={{ ...inputStyle, flex: 1, fontSize: '11px', ...monoStyle }}
          placeholder="output_var_name"
        />
      </div>

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

      {def && (
        <div style={{ fontSize: '10px', color: '#475569', marginTop: 4, fontStyle: 'italic' }}>
          {def.description}
        </div>
      )}
    </div>
  );
}

// ── TransformPanel ────────────────────────────────────────────────────────────

export function TransformPanel({ cfg, updateStepConfig, availableVars }: TransformPanelProps) {
  const [tab, setTab] = useState<'build' | 'test' | 'ai'>('build');
  const [catalog, setCatalog] = useState<{ functions: FunctionDef[]; by_category: Record<string, FunctionDef[]> } | null>(null);
  const [catalogError, setCatalogError] = useState('');
  const [testVars, setTestVars] = useState<Record<string, string>>({});
  const [testResults, setTestResults] = useState<StepResult[] | null>(null);
  const [testRunning, setTestRunning] = useState(false);
  const [testError, setTestError] = useState('');

  const functions: FunctionStep[] = (cfg.functions as FunctionStep[]) ?? [];
  const expressions: Record<string, string> = (cfg.expressions as Record<string, string>) ?? {};
  const extractions: Array<{ from_var: string; json_path: string; var: string }> =
    (cfg.extractions as Array<{ from_var: string; json_path: string; var: string }>) ?? [];

  useEffect(() => {
    fetchCatalog().then(setCatalog).catch(e => setCatalogError(e.message));
  }, []);

  const testInputVars = Array.from(new Set([
    ...availableVars,
    ...functions.map(s => s.output_var).filter(Boolean),
  ]));

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
      const data = await api.post<{ steps: StepResult[] }>('/admin/transform-test', { functions, vars });
      setTestResults(data.steps ?? []);
    } catch (e: unknown) {
      setTestError(e instanceof Error ? e.message : String(e));
    } finally {
      setTestRunning(false);
    }
  };

  return (
    <>
      <div style={{ color: C.indigo, fontSize: '10px', fontWeight: 700, letterSpacing: '0.08em', marginBottom: '10px' }}>
        TRANSFORM CONFIG
      </div>

      <div style={{ display: 'flex', gap: 4, marginBottom: '14px' }}>
        <button style={tabBtn(tab === 'build')} onClick={() => setTab('build')}>Build</button>
        <button style={tabBtn(tab === 'test')} onClick={() => setTab('test')}>Test</button>
        <button style={tabBtn(tab === 'ai')} onClick={() => setTab('ai')}>AI Assist</button>
      </div>

      {/* BUILD TAB */}
      {tab === 'build' && (
        <>
          <div style={sectionTitle(C.indigo)}>FUNCTION CHAIN</div>
          {catalogError && <div style={{ fontSize: '11px', color: '#f87171', marginBottom: 8 }}>Could not load function catalog: {catalogError}</div>}
          {functions.map((step, i) => (
            <FunctionRow
              key={i}
              step={step}
              index={i}
              catalog={catalog}
              availableVars={[...availableVars, ...functions.slice(0, i).map(s => s.output_var).filter(Boolean)]}
              onChange={s => { const next = functions.map((x, j) => j === i ? s : x); updateFunctions(next); }}
              onRemove={() => updateFunctions(functions.filter((_, j) => j !== i))}
              onMoveUp={() => { const next = [...functions]; if (i > 0) { [next[i-1], next[i]] = [next[i], next[i-1]]; updateFunctions(next); } }}
              onMoveDown={() => { const next = [...functions]; if (i < next.length-1) { [next[i], next[i+1]] = [next[i+1], next[i]]; updateFunctions(next); } }}
            />
          ))}
          <button onClick={() => updateFunctions([...functions, { fn: '', input_var: '', output_var: '', args: {} }])}
            style={{ background: 'transparent', border: `1px dashed ${C.outline}`, color: C.textMuted, padding: '6px 10px', borderRadius: '4px', cursor: 'pointer', fontSize: '11px', width: '100%', marginBottom: 16 }}>
            + Add function step
          </button>

          <details style={{ marginBottom: 8 }}>
            <summary style={{ ...sectionTitle('#64748b'), cursor: 'pointer', listStyle: 'none' }}>
              ▸ TEMPLATE EXPRESSIONS (legacy)
            </summary>
            <div style={{ fontSize: 11, color: '#64748b', marginBottom: 8, marginTop: 6 }}>
              Output var → Go template. Use <code style={{ color: C.cyan }}>{'{{.var}}'}</code>.
            </div>
            {Object.entries(expressions).map(([k, v], i) => (
              <div key={i} style={rowStyle}>
                <input value={k} onChange={e => { const ent = Object.entries(expressions); ent[i] = [e.target.value, v]; updateStepConfig('expressions', Object.fromEntries(ent)); }} style={{ ...inputStyle, flex: '0 0 90px', fontSize: '11px', ...monoStyle }} placeholder="output_var" />
                <input value={v} onChange={e => updateStepConfig('expressions', { ...expressions, [k]: e.target.value })} style={{ ...inputStyle, flex: 1, fontSize: '11px' }} placeholder="Hello, {{.user_query}}!" />
                <button onClick={() => { const ex = { ...expressions }; delete ex[k]; updateStepConfig('expressions', ex); }} style={{ background: 'transparent', border: 'none', color: '#f87171', cursor: 'pointer', fontSize: '14px' }}>×</button>
              </div>
            ))}
            <button onClick={() => updateStepConfig('expressions', { ...expressions, '': '' })} style={{ marginTop: 4, background: 'transparent', border: `1px dashed ${C.outline}`, color: C.textMuted, padding: '4px 10px', borderRadius: '4px', cursor: 'pointer', fontSize: '11px', width: '100%' }}>+ Add expression</button>
          </details>

          <details>
            <summary style={{ ...sectionTitle('#64748b'), cursor: 'pointer', listStyle: 'none' }}>
              ▸ JSON EXTRACTIONS (legacy)
            </summary>
            <div style={{ fontSize: 11, color: '#64748b', marginBottom: 8, marginTop: 6 }}>
              Parse a JSON var and extract fields by dot-path.
            </div>
            {extractions.map((ext, i) => (
              <div key={i} style={{ ...rowStyle, alignItems: 'center' }}>
                <input value={ext.from_var} onChange={e => { const next = extractions.map((x, j) => j === i ? { ...x, from_var: e.target.value } : x); updateStepConfig('extractions', next); }} style={{ ...inputStyle, flex: '0 0 80px', fontSize: '11px', ...monoStyle }} placeholder="from_var" />
                <input value={ext.json_path} onChange={e => { const next = extractions.map((x, j) => j === i ? { ...x, json_path: e.target.value } : x); updateStepConfig('extractions', next); }} style={{ ...inputStyle, flex: 1, fontSize: '11px', ...monoStyle }} placeholder="$.field" />
                <span style={{ color: '#64748b', fontSize: '11px' }}>→</span>
                <input value={ext.var} onChange={e => { const next = extractions.map((x, j) => j === i ? { ...x, var: e.target.value } : x); updateStepConfig('extractions', next); }} style={{ ...inputStyle, flex: '0 0 80px', fontSize: '11px', ...monoStyle }} placeholder="out_var" />
                <button onClick={() => updateStepConfig('extractions', extractions.filter((_, j) => j !== i))} style={{ background: 'transparent', border: 'none', color: '#f87171', cursor: 'pointer', fontSize: '14px' }}>×</button>
              </div>
            ))}
            <button onClick={() => updateStepConfig('extractions', [...extractions, { from_var: '', json_path: '', var: '' }])} style={{ marginTop: 4, background: 'transparent', border: `1px dashed ${C.outline}`, color: C.textMuted, padding: '4px 10px', borderRadius: '4px', cursor: 'pointer', fontSize: '11px', width: '100%' }}>+ Add extraction</button>
          </details>
        </>
      )}

      {/* TEST TAB */}
      {tab === 'test' && (
        <>
          <div style={{ fontSize: '11px', color: '#64748b', marginBottom: 10, padding: '8px', background: 'rgba(99,102,241,0.06)', borderRadius: 6, border: `1px solid ${C.outline}` }}>
            Runs the exact same Go code used in production. Paste input var values, then click Run.
          </div>

          <div style={sectionTitle(C.indigo)}>INPUT VARS</div>
          {testInputVars.length === 0 && (
            <div style={{ fontSize: '11px', color: C.textMuted, marginBottom: 8 }}>No vars detected — add function steps in Build tab first.</div>
          )}
          {testInputVars.map(varName => (
            <div key={varName} style={{ marginBottom: 8 }}>
              <label style={{ ...labelStyle, marginBottom: 2 }}>
                <code style={{ color: C.cyan, fontSize: '11px' }}>{`{{.${varName}}}`}</code>
              </label>
              <textarea
                value={testVars[varName] ?? ''}
                onChange={e => setTestVars(prev => ({ ...prev, [varName]: e.target.value }))}
                style={{ width: '100%', minHeight: 60, background: '#0f172a', border: `1px solid ${C.outline}`, borderRadius: 4, color: C.text, padding: '6px 8px', fontSize: '11px', fontFamily: 'JetBrains Mono, monospace', resize: 'vertical', boxSizing: 'border-box' }}
                placeholder={`Value for ${varName}…`}
              />
            </div>
          ))}
          <button
            onClick={() => { const name = prompt('Variable name:'); if (name?.trim()) setTestVars(prev => ({ ...prev, [name.trim()]: '' })); }}
            style={{ background: 'transparent', border: `1px dashed ${C.outline}`, color: C.textMuted, padding: '4px 10px', borderRadius: 4, cursor: 'pointer', fontSize: '11px', marginBottom: 12 }}
          >+ Add var</button>

          <button
            onClick={runTest}
            disabled={testRunning || functions.length === 0}
            style={{ width: '100%', padding: '8px', borderRadius: 6, border: 'none', background: functions.length === 0 ? '#334155' : C.indigo, color: '#fff', fontWeight: 700, fontSize: '12px', cursor: functions.length === 0 ? 'not-allowed' : 'pointer', marginBottom: 14 }}
          >
            {testRunning ? 'Running…' : '▶ Run Test'}
          </button>
          {functions.length === 0 && <div style={{ fontSize: '11px', color: C.textMuted, textAlign: 'center', marginBottom: 10 }}>Add function steps in the Build tab first.</div>}
          {testError && <div style={{ fontSize: '11px', color: '#f87171', marginBottom: 8 }}>{testError}</div>}

          {testResults && (
            <>
              <div style={sectionTitle(C.indigo)}>STEP RESULTS</div>
              {testResults.map((step, i) => (
                <div key={i} style={stepCard(step.ok)}>
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 4 }}>
                    <span style={{ fontSize: '11px', fontWeight: 700, color: step.ok ? '#4ade80' : '#f87171' }}>
                      {i + 1}. {step.fn}
                    </span>
                    <span style={{ fontSize: '10px', color: C.textMuted }}>
                      {step.ok ? '✓' : '✗'} {(step.duration_ns / 1_000_000).toFixed(2)}ms
                    </span>
                  </div>
                  <div style={{ fontSize: '10px', color: '#64748b', marginBottom: 2 }}>
                    <code style={{ color: '#94a3b8', ...monoStyle }}>{step.input_var}</code>
                    {' → '}
                    <code style={{ color: '#94a3b8', ...monoStyle }}>{step.output_var}</code>
                  </div>
                  <pre style={{ margin: 0, fontSize: '10px', color: '#94a3b8', whiteSpace: 'pre-wrap', wordBreak: 'break-all', maxHeight: 120, overflow: 'auto' }}>
                    {step.in?.length > 200 ? step.in.slice(0, 200) + '…' : step.in}
                  </pre>
                  {step.ok && step.out !== undefined && (
                    <>
                      <div style={{ fontSize: '10px', color: '#4ade80', margin: '4px 0 2px' }}>→</div>
                      <pre style={{ margin: 0, fontSize: '10px', color: '#4ade80', whiteSpace: 'pre-wrap', wordBreak: 'break-all', maxHeight: 120, overflow: 'auto' }}>
                        {step.out.length > 200 ? step.out.slice(0, 200) + '…' : step.out}
                      </pre>
                    </>
                  )}
                  {!step.ok && step.error && (
                    <div style={{ fontSize: '10px', color: '#f87171', marginTop: 4 }}>✗ {step.error}</div>
                  )}
                </div>
              ))}
            </>
          )}
        </>
      )}

      {/* AI ASSIST TAB */}
      {tab === 'ai' && (
        <div style={{ textAlign: 'center', padding: '32px 16px', border: `1px dashed ${C.outline}`, borderRadius: 8 }}>
          <div style={{ fontSize: '24px', marginBottom: 8 }}>🤖</div>
          <div style={{ fontSize: '13px', fontWeight: 700, color: C.text, marginBottom: 6 }}>AI Transform Assistant</div>
          <div style={{ fontSize: '11px', color: C.textMuted, lineHeight: 1.6 }}>
            Describe what you want to do and the assistant will suggest a function chain.<br />
            <span style={{ color: '#475569' }}>Coming soon.</span>
          </div>
        </div>
      )}
    </>
  );
}
