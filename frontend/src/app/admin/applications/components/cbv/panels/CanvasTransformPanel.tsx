'use client';
import { useState, useEffect } from 'react';
import type { Node, Edge } from '@xyflow/react';
import type { InlineNodeData } from '../../../types';
import { C } from '../../../constants';
import { fieldStyle, selectStyle, sectionHdrStyle } from './panelShared';
import { InlinePortsSection } from './InlinePortsSection';
import { NodeVarsSection } from './NodeVarsSection';
import { api } from '@/lib/api';

// ── Types ─────────────────────────────────────────────────────────────────────

interface ArgDef { key: string; description: string; required: boolean; default?: string }
interface FunctionDef { name: string; category: string; description: string; args: ArgDef[] }
interface FunctionStep { fn: string; input_var: string; output_var: string; args?: Record<string, string> }

// ── Catalog (cached per session) ─────────────────────────────────────────────

let _catalog: { functions: FunctionDef[]; by_category: Record<string, FunctionDef[]> } | null = null;

async function loadCatalog() {
  if (_catalog) return _catalog;
  _catalog = await api.get('/admin/transform-functions');
  return _catalog!;
}

// ── Recipe shortcuts — most common patterns pre-filled ───────────────────────

const RECIPES: { label: string; step: Partial<FunctionStep> }[] = [
  { label: 'Extract JSON field', step: { fn: 'json_path', args: { path: '$.field' } } },
  { label: 'Strip LLM fences',  step: { fn: 'strip_fences', args: {} } },
  { label: 'Regex extract',     step: { fn: 'regex_extract', args: { pattern: '', group: '1' } } },
  { label: 'Custom step',       step: { fn: '' } },
];

// ── StepCard ─────────────────────────────────────────────────────────────────

function StepCard({
  step, index, catalog, availableVars, onChange, onRemove, onMoveUp, onMoveDown,
}: {
  step: FunctionStep;
  index: number;
  catalog: { functions: FunctionDef[]; by_category: Record<string, FunctionDef[]> } | null;
  availableVars: string[];
  onChange: (s: FunctionStep) => void;
  onRemove: () => void;
  onMoveUp: () => void;
  onMoveDown: () => void;
}) {
  const def = catalog?.functions.find(f => f.name === step.fn);

  return (
    <div style={{ background: 'rgba(139,92,246,0.06)', border: '1px solid rgba(139,92,246,0.22)', borderRadius: 8, padding: '10px 12px', marginBottom: 6 }}>
      {/* Header row */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 4, marginBottom: 8 }}>
        <span style={{ fontSize: 11, color: C.textMuted, minWidth: 18, fontWeight: 700 }}>{index + 1}</span>
        <select
          value={step.fn}
          onChange={e => onChange({ ...step, fn: e.target.value, args: {} })}
          style={{ ...selectStyle, flex: 1, fontSize: 12, padding: '5px 8px' }}
        >
          <option value="">— pick function —</option>
          {catalog && Object.entries(catalog.by_category).map(([cat, fns]) => (
            <optgroup key={cat} label={cat.toUpperCase()}>
              {fns.map(f => <option key={f.name} value={f.name}>{f.name}</option>)}
            </optgroup>
          ))}
        </select>
        <button onClick={onMoveUp}   title="Move up"   style={iconBtn}>↑</button>
        <button onClick={onMoveDown} title="Move down" style={iconBtn}>↓</button>
        <button onClick={onRemove}   title="Remove"    style={{ ...iconBtn, color: '#f87171' }}>×</button>
      </div>

      {/* Description */}
      {def?.description && (
        <div style={{ fontSize: 10, color: '#475569', marginBottom: 8, fontStyle: 'italic' }}>{def.description}</div>
      )}

      {/* in: var picker */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 6 }}>
        <span style={{ fontSize: 10, color: C.textMuted, width: 52, flexShrink: 0 }}>reads from</span>
        <select
          value={step.input_var}
          onChange={e => onChange({ ...step, input_var: e.target.value })}
          style={{ ...selectStyle, flex: 1, fontSize: 12, padding: '5px 8px', fontFamily: 'JetBrains Mono, monospace' }}
        >
          <option value="">— pick var —</option>
          {[...new Set(availableVars)].map(v => <option key={v} value={v}>{v}</option>)}
        </select>
      </div>

      {/* fn args */}
      {def?.args.map(arg => (
        <div key={arg.key} style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 6 }}>
          <span style={{ fontSize: 10, color: C.textMuted, width: 52, flexShrink: 0 }} title={arg.description}>
            {arg.key}{arg.required && <span style={{ color: '#f87171' }}> *</span>}
          </span>
          <input
            value={step.args?.[arg.key] ?? ''}
            onChange={e => onChange({ ...step, args: { ...step.args, [arg.key]: e.target.value } })}
            style={{ ...fieldStyle, flex: 1, fontSize: 12, padding: '5px 8px', fontFamily: 'JetBrains Mono, monospace' }}
            placeholder={arg.default ?? arg.description}
          />
        </div>
      ))}

      {/* writes to */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
        <span style={{ fontSize: 10, color: C.textMuted, width: 52, flexShrink: 0 }}>writes to</span>
        <input
          value={step.output_var}
          onChange={e => onChange({ ...step, output_var: e.target.value })}
          style={{ ...fieldStyle, flex: 1, fontSize: 12, padding: '5px 8px', fontFamily: 'JetBrains Mono, monospace' }}
          placeholder="output_var_name"
        />
      </div>
    </div>
  );
}

const iconBtn: React.CSSProperties = {
  background: 'transparent', border: 'none', color: C.textMuted,
  cursor: 'pointer', fontSize: 14, padding: '0 3px', lineHeight: 1,
};

// ── CanvasTransformPanel ──────────────────────────────────────────────────────

interface Props {
  selectedNode: Node;
  nodes: Node[];
  edges: Edge[];
  setNodes: (updater: (ns: Node[]) => Node[]) => void;
  setIsDirty: (v: boolean) => void;
}

export function CanvasTransformPanel({ selectedNode, nodes, edges, setNodes, setIsDirty }: Props) {
  const [catalog, setCatalog] = useState<{ functions: FunctionDef[]; by_category: Record<string, FunctionDef[]> } | null>(null);
  const [catalogError, setCatalogError] = useState('');

  useEffect(() => { loadCatalog().then(setCatalog).catch(e => setCatalogError(e.message)); }, []);

  const liveNode = nodes.find(n => n.id === selectedNode.id);
  const d = (liveNode?.data ?? selectedNode.data) as unknown as InlineNodeData;
  const cfg = (d.config ?? {}) as Record<string, unknown>;
  const functions: FunctionStep[] = (cfg.functions as FunctionStep[]) ?? [];

  function updateFunctions(next: FunctionStep[]) {
    setNodes(ns => ns.map(n => {
      if (n.id !== selectedNode.id) return n;
      const nd = n.data as unknown as InlineNodeData;
      return { ...n, data: { ...nd, config: { ...(nd.config ?? {}), functions: next } } as unknown as Record<string, unknown> };
    }));
    setIsDirty(true);
  }

  function updateDisplayName(name: string) {
    setNodes(ns => ns.map(n => n.id !== selectedNode.id ? n : { ...n, data: { ...n.data, display_name: name } }));
    setIsDirty(true);
  }

  // Available vars for step N = chain outputs of steps 0..N-1 unioned with node-level inputs
  function availableVarsForStep(stepIndex: number): string[] {
    const chainOut = functions.slice(0, stepIndex).map(s => s.output_var).filter(Boolean);
    return [...new Set([...chainOut, 'input', 'output'])];
  }

  return (
    <div style={{ padding: 16, display: 'flex', flexDirection: 'column', gap: 14, overflowY: 'auto' }}>
      <div style={{ fontSize: 12, fontWeight: 700, color: '#8b5cf6', textTransform: 'uppercase', letterSpacing: '0.06em' }}>
        Transform
      </div>
      <div style={{ fontSize: 11, color: C.textMuted }}>
        Manipulate flow variables without an LLM — extract JSON fields, strip formatting, regex, and more.
        Results from each step are immediately available to the next.
      </div>

      {/* Display name */}
      <div>
        <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Display Name</label>
        <input
          style={fieldStyle}
          value={d.display_name ?? ''}
          onChange={e => updateDisplayName(e.target.value)}
        />
      </div>

      <InlinePortsSection selectedNode={selectedNode} nodes={nodes} edges={edges} setNodes={setNodes} wirings={[]} />

      {/* Recipe shortcuts */}
      <div>
        <div style={{ ...sectionHdrStyle, marginBottom: 8 }}>Quick add</div>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
          {RECIPES.map(r => (
            <button
              key={r.label}
              onClick={() => updateFunctions([...functions, { fn: r.step.fn ?? '', input_var: '', output_var: '', args: r.step.args ?? {} }])}
              style={{ fontSize: 11, padding: '4px 10px', borderRadius: 20, border: '1px solid rgba(139,92,246,0.4)', background: 'rgba(139,92,246,0.08)', color: '#c4b5fd', cursor: 'pointer' }}
            >
              {r.label}
            </button>
          ))}
        </div>
      </div>

      {/* Function chain */}
      <div>
        <div style={{ ...sectionHdrStyle, marginBottom: 8 }}>Function steps</div>
        {catalogError && (
          <div style={{ fontSize: 11, color: '#f87171', marginBottom: 8 }}>Could not load function catalog: {catalogError}</div>
        )}
        {functions.length === 0 && (
          <div style={{ fontSize: 11, color: C.textMuted, textAlign: 'center', padding: '16px 0', border: '1px dashed rgba(139,92,246,0.25)', borderRadius: 8 }}>
            Use Quick add above or &ldquo;+ Add step&rdquo; below to build the chain
          </div>
        )}
        {functions.map((step, i) => (
          <StepCard
            key={i}
            step={step}
            index={i}
            catalog={catalog}
            availableVars={availableVarsForStep(i)}
            onChange={s => updateFunctions(functions.map((x, j) => j === i ? s : x))}
            onRemove={() => updateFunctions(functions.filter((_, j) => j !== i))}
            onMoveUp={() => {
              if (i === 0) return;
              const next = [...functions];
              [next[i - 1], next[i]] = [next[i], next[i - 1]];
              updateFunctions(next);
            }}
            onMoveDown={() => {
              if (i >= functions.length - 1) return;
              const next = [...functions];
              [next[i], next[i + 1]] = [next[i + 1], next[i]];
              updateFunctions(next);
            }}
          />
        ))}
        <button
          onClick={() => updateFunctions([...functions, { fn: '', input_var: '', output_var: '', args: {} }])}
          style={{ background: 'transparent', border: '1px dashed rgba(139,92,246,0.35)', color: C.textMuted, padding: '7px 12px', borderRadius: 8, cursor: 'pointer', fontSize: 12, width: '100%', marginTop: 4 }}
        >
          + Add step
        </button>
      </div>

      {/* Reads / Writes */}
      <NodeVarsSection
        readsNote="Reads the input_var of each step from the current flow variables."
        writes={functions.filter(s => s.output_var).map(s => ({ name: s.output_var, note: `written by ${s.fn || 'step'}` }))}
        writesNote={functions.length === 0 ? 'No steps configured yet.' : undefined}
      />

      {/* Debug note */}
      <div style={{ fontSize: 10, color: '#475569', padding: '8px 10px', background: 'rgba(139,92,246,0.06)', borderRadius: 6, borderLeft: '3px solid rgba(139,92,246,0.4)' }}>
        Debug mode shows each step&apos;s input → output in the inspector panel after a run.
      </div>
    </div>
  );
}
