'use client';
import { C, glass } from '../../constants';
import type { NameableField } from './useInlinePortWiring';

// Small drop-point popover for Phase 5 (docs/APPFLOW_NAMED_PORTS_PLAN.md):
// opens when a wire is dropped onto a target with more than one nameable
// field (today, only `llm`: system prompt + user prompt — `condition` never
// shows this, it auto-binds since it has exactly one). Positioned at the
// raw drop coordinates from the connect gesture, not tied to any node's
// on-canvas position, matching the plan's "small popover at the drop point"
// spec confirmed with the user.

interface Props {
  x: number;
  y: number;
  fields: NameableField[];
  onPick: (field: NameableField) => void;
  onDismiss: () => void;
}

export function PortBindingPopover({ x, y, fields, onPick, onDismiss }: Props) {
  return (
    <>
      <div
        style={{ position: 'fixed', inset: 0, zIndex: 999 }}
        onClick={onDismiss}
      />
      <div
        style={{
          position: 'fixed', left: x, top: y, zIndex: 1000,
          ...glass, borderRadius: 10, padding: 6, minWidth: 160,
        }}
      >
        <div style={{ fontSize: 10, color: C.textMuted, padding: '2px 6px 6px', textTransform: 'uppercase', letterSpacing: '0.06em' }}>
          Bind to which field?
        </div>
        {fields.map(f => (
          <button
            key={f.key}
            onClick={() => onPick(f)}
            style={{
              display: 'block', width: '100%', textAlign: 'left', padding: '6px 8px',
              borderRadius: 6, border: 'none', background: 'transparent', color: C.text,
              fontSize: 12, cursor: 'pointer',
            }}
            onMouseEnter={e => (e.currentTarget.style.background = 'rgba(0,240,255,0.08)')}
            onMouseLeave={e => (e.currentTarget.style.background = 'transparent')}
          >
            {f.label}
          </button>
        ))}
      </div>
    </>
  );
}
