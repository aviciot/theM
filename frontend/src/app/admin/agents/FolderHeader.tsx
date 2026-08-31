'use client';
import { useEffect, useRef, useState } from 'react';
import type { Agent } from '@/lib/api';
import type { AgentFolder } from './agentTypes';
import { agentCategory, categoryAccent, agentIcon } from './agentUtils';

export function FolderHeader({
  folder,
  folderAgents,
  count,
  isDragOver,
  onToggleCollapse,
  onRename,
  onDragOver,
  onDragLeave,
  onDrop,
}: {
  folder: AgentFolder;
  folderAgents: Agent[];
  count: number;
  isDragOver: boolean;
  onToggleCollapse: () => void;
  onRename: (name: string) => void;
  onDragOver: (e: React.DragEvent) => void;
  onDragLeave: () => void;
  onDrop: (e: React.DragEvent) => void;
}) {
  const [editing, setEditing] = useState(false);
  const [editVal, setEditVal] = useState(folder.name);
  const inputRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (editing && inputRef.current) inputRef.current.focus();
  }, [editing]);

  function commitRename() {
    const v = editVal.trim();
    if (v) onRename(v);
    else setEditVal(folder.name);
    setEditing(false);
  }

  function startRename(e: React.MouseEvent) {
    e.stopPropagation();
    setEditVal(folder.name);
    setEditing(true);
  }

  const previewAgents = folderAgents.slice(0, 4);

  if (folder.collapsed) {
    const slots = [0, 1, 2, 3];
    return (
      <div
        className="chroma-card"
        onClick={onToggleCollapse}
        onDragOver={onDragOver}
        onDragLeave={onDragLeave}
        onDrop={onDrop}
        style={{
          position: 'relative',
          display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center',
          gap: '10px',
          padding: '20px 16px 16px',
          borderRadius: '20px',
          ['--card-border' as string]: isDragOver ? 'rgba(0,209,255,0.45)' : 'rgba(91,127,255,0.35)',
          ['--card-gradient' as string]: isDragOver
            ? 'linear-gradient(160deg, rgba(0,209,255,0.10) 0%, rgba(0,209,255,0.04) 100%)'
            : 'linear-gradient(160deg, rgba(91,127,255,0.09) 0%, rgba(91,127,255,0.03) 100%)',
          background: isDragOver
            ? 'linear-gradient(160deg, rgba(0,209,255,0.13) 0%, rgba(0,209,255,0.05) 100%)'
            : 'linear-gradient(160deg, rgba(255,255,255,0.14) 0%, rgba(255,255,255,0.04) 60%, rgba(91,127,255,0.06) 100%)',
          border: `1px solid ${isDragOver ? 'rgba(0,209,255,0.55)' : 'rgba(255,255,255,0.22)'}`,
          backdropFilter: 'blur(28px) saturate(1.6)',
          WebkitBackdropFilter: 'blur(28px) saturate(1.6)',
          boxShadow: isDragOver
            ? '0 0 0 2px rgba(0,209,255,0.18), 0 8px 32px rgba(0,0,0,0.4)'
            : '0 8px 32px rgba(0,0,0,0.4), inset 0 1.5px 0 rgba(255,255,255,0.28), inset 0 -1px 0 rgba(0,0,0,0.18)',
          cursor: 'pointer',
          transition: 'background 180ms ease, border-color 180ms ease, box-shadow 180ms ease',
          userSelect: 'none',
          minHeight: '160px',
        }}
      >
        <div style={{ position: 'absolute', top: 0, left: 0, right: 0, height: '55%', borderRadius: '20px 20px 60% 60% / 20px 20px 40px 40px', background: 'linear-gradient(180deg, rgba(255,255,255,0.13) 0%, rgba(255,255,255,0.03) 60%, transparent 100%)', pointerEvents: 'none' }} />
        <div style={{ position: 'absolute', top: 0, left: '16px', right: '16px', height: '1px', background: 'linear-gradient(90deg, transparent, rgba(255,255,255,0.45), transparent)', pointerEvents: 'none' }} />

        <div style={{ position: 'absolute', top: '10px', right: '12px', fontSize: '10px', fontWeight: 700, color: 'rgba(255,255,255,0.5)', background: 'rgba(255,255,255,0.09)', border: '1px solid rgba(255,255,255,0.12)', borderRadius: '9999px', padding: '1px 6px', lineHeight: '16px' }}>{count}</div>

        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '6px', padding: '10px', background: 'rgba(0,0,0,0.18)', borderRadius: '16px', border: '1px solid rgba(255,255,255,0.07)', boxShadow: 'inset 0 2px 8px rgba(0,0,0,0.25)' }}>
          {slots.map(i => {
            const a = previewAgents[i];
            if (!a) {
              return <div key={i} style={{ width: '36px', height: '36px', borderRadius: '9px', background: 'rgba(255,255,255,0.04)', border: '1px solid rgba(255,255,255,0.06)' }} />;
            }
            const cat = agentCategory(a);
            const acc = categoryAccent(cat);
            const ico = agentIcon(a, cat);
            return (
              <div key={a.id} style={{ width: '36px', height: '36px', borderRadius: '9px', background: `radial-gradient(circle at 30% 25%, ${acc.glow}, transparent 65%), linear-gradient(145deg, rgba(20,32,52,0.97), rgba(8,16,30,0.97))`, border: `1px solid ${acc.border}`, boxShadow: `0 0 10px ${acc.glow}`, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                <span className="material-symbols-outlined" style={{ fontSize: '16px', color: acc.color }}>{ico}</span>
              </div>
            );
          })}
        </div>

        {editing ? (
          <input
            ref={inputRef}
            value={editVal}
            onChange={(e) => setEditVal(e.target.value)}
            onBlur={commitRename}
            onKeyDown={(e) => { if (e.key === 'Enter') commitRename(); if (e.key === 'Escape') { setEditVal(folder.name); setEditing(false); } }}
            onClick={(e) => e.stopPropagation()}
            style={{ background: 'transparent', border: 'none', borderBottom: '1px solid rgba(0,209,255,0.5)', color: 'var(--tm-card-text)', fontSize: '12px', fontWeight: 600, outline: 'none', padding: '0 2px', textAlign: 'center', width: '100%' }}
          />
        ) : (
          <span onClick={startRename} title="Click to rename" style={{ fontSize: '12px', fontWeight: 600, color: 'rgba(255,255,255,0.75)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', maxWidth: '100%', cursor: 'text', textAlign: 'center' }}>
            {folder.name}
          </span>
        )}
      </div>
    );
  }

  return (
    <div
      onDragOver={onDragOver}
      onDragLeave={onDragLeave}
      onDrop={onDrop}
      style={{
        display: 'flex', alignItems: 'center', gap: '10px',
        padding: '10px 16px',
        background: isDragOver ? 'rgba(0,209,255,0.06)' : 'rgba(255,255,255,0.04)',
        border: `1px solid ${isDragOver ? 'rgba(0,209,255,0.4)' : 'rgba(255,255,255,0.10)'}`,
        borderRadius: '8px 8px 0 0',
        cursor: 'pointer',
        transition: 'background 150ms ease, border-color 150ms ease',
        userSelect: 'none',
      }}
    >
      <span className="material-symbols-outlined" style={{ fontSize: '18px', color: '#94a3b8', flexShrink: 0 }}>
        folder_open
      </span>

      {editing ? (
        <input
          ref={inputRef}
          value={editVal}
          onChange={(e) => setEditVal(e.target.value)}
          onBlur={commitRename}
          onKeyDown={(e) => { if (e.key === 'Enter') commitRename(); if (e.key === 'Escape') { setEditVal(folder.name); setEditing(false); } }}
          onClick={(e) => e.stopPropagation()}
          style={{ flex: 1, background: 'transparent', border: 'none', borderBottom: '1px solid rgba(0,209,255,0.5)', color: 'var(--tm-card-text)', fontSize: '14px', fontWeight: 600, outline: 'none', padding: '0 2px' }}
        />
      ) : (
        <span
          onDoubleClick={(e) => { e.stopPropagation(); setEditVal(folder.name); setEditing(true); }}
          onClick={onToggleCollapse}
          style={{ flex: 1, fontSize: '14px', fontWeight: 600, color: 'var(--tm-card-text)' }}
        >
          {folder.name}
        </span>
      )}

      <span style={{ fontSize: '11px', color: 'var(--tm-card-text-muted)', flexShrink: 0, fontWeight: 600 }}>{count}</span>

      <button
        onClick={(e) => { e.stopPropagation(); onToggleCollapse(); }}
        style={{ background: 'none', border: 'none', cursor: 'pointer', padding: '2px', color: 'var(--tm-card-text-muted)', display: 'flex', alignItems: 'center', flexShrink: 0 }}
      >
        <span className="material-symbols-outlined" style={{ fontSize: '18px' }}>expand_less</span>
      </button>
    </div>
  );
}
