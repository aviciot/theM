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
    return (
      <div
        onClick={onToggleCollapse}
        onDragOver={onDragOver}
        onDragLeave={onDragLeave}
        onDrop={onDrop}
        style={{
          display: 'flex', alignItems: 'center', gap: '10px',
          padding: '0 14px',
          height: '44px',
          background: isDragOver ? 'rgba(0,209,255,0.06)' : 'rgba(255,255,255,0.04)',
          border: `1px solid ${isDragOver ? 'rgba(0,209,255,0.4)' : 'rgba(255,255,255,0.10)'}`,
          borderRadius: '10px',
          cursor: 'pointer',
          transition: 'background 150ms ease, border-color 150ms ease',
          userSelect: 'none',
        }}
      >
        {/* Mini icon previews */}
        <div style={{ display: 'flex', gap: '4px', flexShrink: 0 }}>
          {previewAgents.slice(0, 3).map(a => {
            const cat = agentCategory(a);
            const acc = categoryAccent(cat);
            const ico = agentIcon(a, cat);
            return (
              <div key={a.id} style={{ width: '24px', height: '24px', borderRadius: '6px', background: `radial-gradient(circle at 30% 25%, ${acc.glow}, transparent 65%), linear-gradient(145deg, rgba(20,32,52,0.97), rgba(8,16,30,0.97))`, border: `1px solid ${acc.border}`, display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 }}>
                <span className="material-symbols-outlined" style={{ fontSize: '12px', color: acc.color }}>{ico}</span>
              </div>
            );
          })}
          {count > 3 && (
            <div style={{ width: '24px', height: '24px', borderRadius: '6px', background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.10)', display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: '9px', fontWeight: 700, color: 'var(--tm-card-text-muted)', flexShrink: 0 }}>
              +{count - 3}
            </div>
          )}
        </div>

        {/* Name */}
        {editing ? (
          <input
            ref={inputRef}
            value={editVal}
            onChange={(e) => setEditVal(e.target.value)}
            onBlur={commitRename}
            onKeyDown={(e) => { if (e.key === 'Enter') commitRename(); if (e.key === 'Escape') { setEditVal(folder.name); setEditing(false); } }}
            onClick={(e) => e.stopPropagation()}
            style={{ flex: 1, background: 'transparent', border: 'none', borderBottom: '1px solid rgba(0,209,255,0.5)', color: 'var(--tm-card-text)', fontSize: '13px', fontWeight: 600, outline: 'none', padding: '0 2px' }}
          />
        ) : (
          <span
            onClick={startRename}
            title="Click to rename"
            style={{ flex: 1, fontSize: '13px', fontWeight: 600, color: 'var(--tm-card-text)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', cursor: 'text' }}
          >
            {folder.name}
          </span>
        )}

        <span style={{ fontSize: '11px', color: 'var(--tm-card-text-muted)', flexShrink: 0, fontWeight: 600 }}>{count}</span>
        <span className="material-symbols-outlined" style={{ fontSize: '16px', color: 'var(--tm-card-text-muted)', flexShrink: 0 }}>expand_more</span>
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
