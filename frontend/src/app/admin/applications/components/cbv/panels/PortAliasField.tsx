'use client';
import { useState, useEffect, useRef } from 'react';

// Click-to-rename inline field for a drag-created named port alias. Ported
// from the agent builder's StepDataFlowSection.tsx (same component, same
// sanitization rule) per docs/APPFLOW_NAMED_PORTS_PLAN.md Phase 5 — the
// rename UI is generic, only the commit callback differs (AppFlow rewrites
// `{{.alias}}` template text instead of agentgen's {from_step, from_port}
// binding record).

interface Props {
  nodeId: string;
  alias: string;
  onRename: (nodeId: string, oldAlias: string, newAlias: string) => void;
}

export function PortAliasField({ nodeId, alias, onRename }: Props) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(alias);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => { setDraft(alias); }, [alias]);
  useEffect(() => { if (editing) inputRef.current?.select(); }, [editing]);

  function commit() {
    setEditing(false);
    const clean = draft.trim().replace(/[^a-z0-9_]/gi, '_').replace(/^[0-9]/, '_$&');
    if (clean && clean !== alias) onRename(nodeId, alias, clean);
    else setDraft(alias);
  }

  if (!editing) {
    return (
      <div
        onClick={() => setEditing(true)}
        title="Click to rename port alias"
        style={{ padding: '3px 8px 5px', fontSize: 10, color: '#475569', fontFamily: 'monospace', cursor: 'text', borderTop: '1px dashed rgba(0,240,255,0.1)', display: 'flex', alignItems: 'center', gap: 4 }}
      >
        <span style={{ color: '#334155', fontSize: 9 }}>alias:</span>
        <span style={{ color: '#7dd3fc' }}>{alias}</span>
        <span style={{ color: '#334155', fontSize: 9, marginLeft: 2 }}>✎</span>
      </div>
    );
  }
  return (
    <div style={{ padding: '3px 8px 5px', borderTop: '1px dashed rgba(0,240,255,0.1)' }}>
      <input
        ref={inputRef}
        value={draft}
        onChange={e => setDraft(e.target.value)}
        onBlur={commit}
        onKeyDown={e => { if (e.key === 'Enter') commit(); if (e.key === 'Escape') { setEditing(false); setDraft(alias); } }}
        style={{ width: '100%', background: 'rgba(0,240,255,0.06)', border: '1px solid rgba(0,240,255,0.3)', color: '#7dd3fc', fontSize: 10, fontFamily: 'monospace', padding: '2px 4px', borderRadius: 3, outline: 'none', boxSizing: 'border-box' }}
      />
    </div>
  );
}
