'use client';
import { C } from '../../../constants';

// ── NodeVarsSection — READS / WRITES info block for flow-control nodes ────────
//
// Static documentation of what variables a node consumes and produces.
// Read-only — helps users know what to reference in downstream nodes.

interface VarEntry {
  name: string;       // the {{.varname}} key
  note?: string;      // short description shown beside it
}

interface Props {
  reads?: VarEntry[];
  writes?: VarEntry[];
}

const varChip = (v: VarEntry, color: string) => (
  <div key={v.name} style={{ display: 'flex', alignItems: 'baseline', gap: 8, padding: '4px 8px', borderRadius: 6, background: `${color}0d`, border: `1px solid ${color}26`, marginBottom: 4 }}>
    <code style={{ color, fontSize: 11, fontFamily: 'JetBrains Mono, monospace', flexShrink: 0 }}>{`{{.${v.name}}}`}</code>
    {v.note && <span style={{ color: C.textMuted, fontSize: 10 }}>{v.note}</span>}
  </div>
);

export function NodeVarsSection({ reads, writes }: Props) {
  if (!reads?.length && !writes?.length) return null;
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10, borderTop: '1px solid rgba(255,255,255,0.08)', paddingTop: 12 }}>
      {reads && reads.length > 0 && (
        <div>
          <div style={{ fontSize: 11, fontWeight: 700, letterSpacing: '0.08em', color: '#f97316', marginBottom: 6, textTransform: 'uppercase' }}>Reads</div>
          {reads.map(v => varChip(v, '#f97316'))}
        </div>
      )}
      {writes && writes.length > 0 && (
        <div>
          <div style={{ fontSize: 11, fontWeight: 700, letterSpacing: '0.08em', color: C.cyan, marginBottom: 6, textTransform: 'uppercase' }}>Writes</div>
          {writes.map(v => varChip(v, C.cyan))}
        </div>
      )}
    </div>
  );
}
