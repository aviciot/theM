import type { Node, Edge } from '@xyflow/react';
import type { StepData } from '../types';
import { ctxItemStyle } from '../constants';

export type CtxTarget =
  | { kind: 'node'; node: Node }
  | { kind: 'edge'; edge: Edge };

interface NodeContextMenuProps {
  ctxMenu: { x: number; y: number; target: CtxTarget };
  closeCtx: () => void;
  ctxDelete: () => void;
  ctxEditPipeline: () => void;
  setSelectedNode: (node: Node | null) => void;
}

export function NodeContextMenu({ ctxMenu, closeCtx, ctxDelete, ctxEditPipeline, setSelectedNode }: NodeContextMenuProps) {
  return (
    <div
      onMouseLeave={closeCtx}
      style={{
        position: 'fixed', zIndex: 9999,
        left: ctxMenu.x, top: ctxMenu.y,
        background: '#1e293b', border: '1px solid #334155',
        borderRadius: '8px', padding: '4px', minWidth: '160px',
        boxShadow: '0 8px 24px rgba(0,0,0,0.5)',
      }}
    >
      {ctxMenu.target.kind === 'node' && (
        <>
          <div style={{ padding: '4px 8px', fontSize: '10px', color: '#64748b', fontWeight: 700, textTransform: 'uppercase', letterSpacing: '0.08em' }}>
            {ctxMenu.target.node.type === 'agentRoot' ? 'Agent' : ctxMenu.target.node.type === 'skill' ? 'Skill' : (ctxMenu.target.node.data as unknown as StepData).step_type}
          </div>
          <button onClick={() => { setSelectedNode(ctxMenu.target.kind === 'node' ? ctxMenu.target.node : null); closeCtx(); }} style={ctxItemStyle}>
            ✏️ Properties
          </button>
          {ctxMenu.target.node.type === 'skill' && (
            <button onClick={ctxEditPipeline} style={ctxItemStyle}>
              ⚡ Edit Pipeline
            </button>
          )}
          <div style={{ borderTop: '1px solid #334155', margin: '4px 0' }} />
        </>
      )}
      <button onClick={ctxDelete} style={{ ...ctxItemStyle, color: '#f87171' }}>
        🗑️ Delete
      </button>
    </div>
  );
}
