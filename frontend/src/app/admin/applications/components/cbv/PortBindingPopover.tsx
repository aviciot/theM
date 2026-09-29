'use client';
import type { NameableField } from './useInlinePortWiring';
import { ChoicePopover } from './ChoicePopover';

// Small drop-point popover for Phase 5 (docs/APPFLOW_NAMED_PORTS_PLAN.md):
// opens when a wire is dropped onto a target with more than one nameable
// field (today, only `llm`: system prompt + user prompt — `condition` never
// shows this, it auto-binds since it has exactly one). Positioned at the
// raw drop coordinates from the connect gesture, not tied to any node's
// on-canvas position, matching the plan's "small popover at the drop point"
// spec confirmed with the user.
//
// Thin typed wrapper over ChoicePopover (docs/APPFLOW_GUARD_OUTPUT_PORTS_PLAN.md
// Phase 2 factored the shared visual shell out so the new source-side
// popover — SourcePortPopover.tsx — looks and feels identical without
// duplicating the styling).

interface Props {
  x: number;
  y: number;
  fields: NameableField[];
  onPick: (field: NameableField) => void;
  onDismiss: () => void;
}

export function PortBindingPopover({ x, y, fields, onPick, onDismiss }: Props) {
  return (
    <ChoicePopover
      x={x}
      y={y}
      title="Bind to which field?"
      items={fields}
      onPick={item => onPick(fields.find(f => f.key === item.key)!)}
      onDismiss={onDismiss}
    />
  );
}
