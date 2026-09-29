'use client';
import type { DragSource } from './useInlinePortWiring';
import { ChoicePopover } from './ChoicePopover';

// New for docs/APPFLOW_GUARD_OUTPUT_PORTS_PLAN.md Phase 2 — the SOURCE-side
// counterpart to PortBindingPopover.tsx's pre-existing target-side "which
// field?" popover. Opens when a drag's SOURCE node has more than one
// draggable output (its own primary var AND ≥1 guard var, or ≥2 guard
// vars) — asks "which output are you sending?" instead of "which field are
// you receiving into?". Same visual shell (ChoicePopover), different node
// inspected, different moment in the gesture.

interface Props {
  x: number;
  y: number;
  sources: DragSource[];
  onPick: (source: DragSource) => void;
  onDismiss: () => void;
}

export function SourcePortPopover({ x, y, sources, onPick, onDismiss }: Props) {
  return (
    <ChoicePopover
      x={x}
      y={y}
      title="Send which output?"
      items={sources.map(s => ({ key: s.varName, label: s.label }))}
      onPick={item => onPick(sources.find(s => s.varName === item.key)!)}
      onDismiss={onDismiss}
    />
  );
}
