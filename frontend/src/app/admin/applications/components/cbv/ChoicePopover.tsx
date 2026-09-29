'use client';
import { C, glass } from '../../constants';

// ── ChoicePopover — shared shell for both the target-side ("which field?")
// and source-side ("which output?", docs/APPFLOW_GUARD_OUTPUT_PORTS_PLAN.md
// Phase 2) drag-to-wire popovers. Same visual card either caller uses; the
// two callers differ only in WHICH node they inspect and WHAT list of
// choices they resolve — this component has no opinion on that, it just
// renders a positioned list of {key, label} and reports back which one was
// picked. Positioned at raw screen coordinates from the connect gesture,
// matching the original PortBindingPopover's spec.

export interface ChoiceItem {
  key: string;
  label: string;
}

interface Props {
  x: number;
  y: number;
  title: string;
  items: ChoiceItem[];
  onPick: (item: ChoiceItem) => void;
  onDismiss: () => void;
}

export function ChoicePopover({ x, y, title, items, onPick, onDismiss }: Props) {
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
          {title}
        </div>
        {items.map(item => (
          <button
            key={item.key}
            onClick={() => onPick(item)}
            style={{
              display: 'block', width: '100%', textAlign: 'left', padding: '6px 8px',
              borderRadius: 6, border: 'none', background: 'transparent', color: C.text,
              fontSize: 12, cursor: 'pointer',
            }}
            onMouseEnter={e => (e.currentTarget.style.background = 'rgba(0,240,255,0.08)')}
            onMouseLeave={e => (e.currentTarget.style.background = 'transparent')}
          >
            {item.label}
          </button>
        ))}
      </div>
    </>
  );
}
