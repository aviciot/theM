import React, { type DragEvent } from 'react';
import { getNodeDef } from '@/lib/nodeRegistry';
import { C } from '../constants';
import { stepMeta } from './StepNode';
import type { AgentStepDoc } from '@/lib/api';

interface NodeLibraryPanelProps {
  activeView: 'agent' | 'skill';
  libraryWidth: number;
  onAddStep: (type: AgentStepDoc['type']) => void;
  onResizeStart: (e: React.MouseEvent) => void;
}

export function NodeLibraryPanel({ activeView, libraryWidth, onAddStep, onResizeStart }: NodeLibraryPanelProps) {
  return (
    <div style={{
      width: libraryWidth, flexShrink: 0, borderRight: `1px solid ${C.outline}`,
      background: C.surface, overflowY: 'auto', display: 'flex', flexDirection: 'column',
      position: 'relative',
    }} className="dark-scrollbar">
      <div style={{ padding: '14px 14px 8px', fontSize: 10, fontWeight: 700, color: C.textMuted, letterSpacing: 1.5, textTransform: 'uppercase', borderBottom: `1px solid ${C.outline}` }}>
        {activeView === 'agent' ? 'Node Library' : 'Step Library'}
      </div>

      <div style={{ padding: '12px 10px', display: 'flex', flexDirection: 'column', gap: 6, flex: 1 }}>
        {activeView === 'agent' ? (
          <>
            <div style={{ fontSize: 10, fontWeight: 700, color: C.textMuted, letterSpacing: 1, textTransform: 'uppercase', marginBottom: 4 }}>Skills</div>
            <div
              draggable
              onDragStart={(e: DragEvent) => { e.dataTransfer.setData('nodeType', 'skill'); e.dataTransfer.effectAllowed = 'move'; }}
              className="palette-card"
              style={{
                display: 'flex', alignItems: 'center', gap: 10, padding: '9px 12px',
                borderRadius: 8, cursor: 'grab', userSelect: 'none',
                background: C.purpleBg, border: `1px solid ${C.purpleBorder}`,
              }}
            >
              <span style={{ fontSize: 18 }}>⚡</span>
              <div>
                <div style={{ fontSize: 13, fontWeight: 600, color: C.purple }}>Skill</div>
                <div style={{ fontSize: 10, color: C.textMuted }}>Named capability</div>
              </div>
            </div>
          </>
        ) : (
          <>
            {[
              { label: 'Data Flow',  items: ['input', 'response'] },
              { label: 'Processing', items: ['llm', 'transform', 'http', 'branch'] },
              { label: 'Advanced',   items: ['loop', 'parallel', 'a2a_call', 'human_wait', 'stream_out'] },
            ].map(group => (
              <div key={group.label}>
                <div style={{ fontSize: 10, fontWeight: 700, color: C.textMuted, letterSpacing: 1, textTransform: 'uppercase', margin: '8px 0 4px' }}>{group.label}</div>
                {group.items.map(type => {
                  const def  = getNodeDef(type);
                  const meta = stepMeta(type);
                  return (
                    <div
                      key={type}
                      draggable
                      title={def.description}
                      onDragStart={(e: DragEvent) => { e.dataTransfer.setData('nodeType', 'step'); e.dataTransfer.setData('stepType', type); e.dataTransfer.effectAllowed = 'move'; }}
                      onClick={() => onAddStep(type as AgentStepDoc['type'])}
                      className="palette-card"
                      style={{
                        display: 'flex', alignItems: 'center', gap: 8, padding: '7px 10px',
                        borderRadius: 7, cursor: 'grab', userSelect: 'none', marginBottom: 3,
                        background: `${meta.border}18`, border: `1px solid ${meta.border}`,
                      }}
                    >
                      <span style={{ fontSize: 18, width: 22, textAlign: 'center', flexShrink: 0 }}>{meta.emoji}</span>
                      <div style={{ minWidth: 0 }}>
                        <div style={{ fontSize: 12, fontWeight: 600, color: meta.border }}>{meta.label}</div>
                        <div style={{ fontSize: 10, color: C.textMuted, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{def.description}</div>
                      </div>
                    </div>
                  );
                })}
              </div>
            ))}
          </>
        )}
      </div>

      <div
        onMouseDown={onResizeStart}
        style={{
          position: 'absolute', top: 0, right: -3, width: 6, height: '100%',
          cursor: 'col-resize', zIndex: 10,
        }}
      />
    </div>
  );
}
