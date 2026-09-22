import type { AppDefinitionDoc } from '@/lib/api';

// ── App canvas Export / Import JSON ──────────────────────────────────────────
// Mirrors the agent builder's existing Export/Import JSON feature
// (frontend/src/app/admin/agents/builder/hooks/useDefinitionLifecycle.ts) —
// pure client-side, no backend endpoint. See docs/APP_CANVAS_EXPORT_IMPORT_PLAN.md.
//
// Deliberately stricter than the agent builder's precedent: the agent builder
// only checks 2 top-level fields and lets a malformed deeper shape surface as
// a raw JS exception message. Here, shapeCheck validates every field
// validateDefinition() enforces server-side, so obviously-broken JSON never
// reaches docToCanvas.

export function exportAppDefinition(doc: AppDefinitionDoc, appSlug: string): void {
  const blob = new Blob([JSON.stringify(doc, null, 2)], { type: 'application/json' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = `${appSlug || 'app'}.json`;
  a.click();
  URL.revokeObjectURL(url);
}

/** Returns an error message if the shape is invalid, or null if it's OK to load. */
export function checkAppDefinitionShape(doc: unknown): string | null {
  if (typeof doc !== 'object' || doc === null) return 'Not a JSON object';
  const d = doc as Record<string, unknown>;

  if (d.schema_version !== 2) return `Expected schema_version 2, got ${JSON.stringify(d.schema_version)}`;
  if (!Array.isArray(d.components)) return 'Missing or invalid "components" array';
  if (!Array.isArray(d.entry_points)) return 'Missing or invalid "entry_points" array';
  if (!Array.isArray(d.connections)) return 'Missing or invalid "connections" array';

  for (const [i, c] of d.components.entries()) {
    if (typeof c !== 'object' || c === null) return `components[${i}] is not an object`;
    const comp = c as Record<string, unknown>;
    if (typeof comp.instance_id !== 'string' || !comp.instance_id) return `components[${i}].instance_id must be a non-empty string`;
    const ref = comp.definition_ref as Record<string, unknown> | undefined;
    if (typeof ref !== 'object' || ref === null) return `components[${i}].definition_ref is missing`;
    if (typeof ref.kind !== 'string' || !ref.kind) return `components[${i}].definition_ref.kind must be a non-empty string`;
    if (typeof ref.name !== 'string' || !ref.name) return `components[${i}].definition_ref.name must be a non-empty string`;
  }

  for (const [i, ep] of d.entry_points.entries()) {
    if (typeof ep !== 'object' || ep === null) return `entry_points[${i}] is not an object`;
    const e = ep as Record<string, unknown>;
    if (typeof e.instance_id !== 'string' || !e.instance_id) return `entry_points[${i}].instance_id must be a non-empty string`;
    if (typeof e.slug !== 'string' || !e.slug) return `entry_points[${i}].slug must be a non-empty string`;
    if (typeof e.protocol !== 'string' || !e.protocol) return `entry_points[${i}].protocol must be a non-empty string`;
  }

  for (const [i, conn] of d.connections.entries()) {
    if (typeof conn !== 'object' || conn === null) return `connections[${i}] is not an object`;
    const c = conn as Record<string, unknown>;
    if (typeof c.source !== 'string' || !c.source) return `connections[${i}].source must be a non-empty string`;
    if (typeof c.target !== 'string' || !c.target) return `connections[${i}].target must be a non-empty string`;
  }

  return null;
}

export function parseImportedAppDefinition(rawText: string): { doc: AppDefinitionDoc } | { error: string } {
  let parsed: unknown;
  try {
    parsed = JSON.parse(rawText);
  } catch (err) {
    return { error: `Not valid JSON: ${err instanceof Error ? err.message : String(err)}` };
  }
  const shapeErr = checkAppDefinitionShape(parsed);
  if (shapeErr) return { error: `Invalid app definition: ${shapeErr}` };
  return { doc: parsed as AppDefinitionDoc };
}
