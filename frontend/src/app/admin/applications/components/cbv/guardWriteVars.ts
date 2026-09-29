/**
 * docs/APPFLOW_GUARD_OUTPUT_PORTS_PLAN.md Phase 2 — the "Writes" panel's
 * guard-var half. Mirrors go/internal/appflow/workflow.go's naming exactly
 * (guardStatusVarName, writeGuardCategoryVars, writeFileGuardVar) so the
 * flat FlowVar key shown here is byte-for-byte what a condition node must
 * type/drag to actually read it — this file is the single place that
 * convention is duplicated into TypeScript, keep it in sync if the Go side
 * changes.
 *
 * Phase 2 decided (b): FlowVars stays a flat map[string]string. The dotted
 * "display" name below (e.g. `pii_guard.email.status`) is presentation
 * only — `flatVar` is the real string a template must reference.
 */

import type { MiddlewareWiring } from '@/lib/api';

export interface GuardWriteVar {
  flatVar: string;     // real FlowVar key, e.g. "agent1_pii_redact_phone_status"
  displayRef: string;  // nice dotted form shown in the UI, e.g. "pii_guard.phone.status"
  guardLabel: string;  // e.g. "PII Guard"
  detail?: string;     // e.g. "category: phone" — shown as a sub-line, not part of the var itself
}

// File Guard's scan-status FlowVar segment is a hardcoded literal in
// workflow.go's writeFileGuardVar ("file_guard", underscore) — independent
// of the real wiring def_slug ("file-guard", hyphen), since its status
// comes from FileGateActivity directly, never through TextGate/Categories.
const FILE_GUARD_VAR_SEGMENT = 'file_guard';

const GUARD_SHORT_LABEL: Record<string, string> = {
  'file-guard': 'file_guard',
  pii_redact: 'pii_guard',
  prompt_inject: 'prompt_guard',
};

/**
 * PII Guard's `categories` config field (db/116) restricts which of the 4
 * fixed categories (email/phone/credit_card/ssn — internal/middleware/pii's
 * CategoryNames()) actually run; empty/omitted means all 4 (fail-open
 * convention, confirmed in docs/APPFLOW_TEXT_GUARDS_PLAN.md). This list must
 * stay in sync with pii.CategoryNames() — there is no live endpoint for it,
 * same tradeoff AgentGuardsSection's config_fields.options already accepts.
 */
const ALL_PII_CATEGORIES = ['email', 'phone', 'credit_card', 'ssn'];

function piiCategoriesFor(wiring: MiddlewareWiring): string[] {
  const configured = wiring.config_override.categories;
  if (Array.isArray(configured) && configured.length > 0) {
    return configured.filter((c): c is string => typeof c === 'string');
  }
  return ALL_PII_CATEGORIES;
}

/**
 * Every FlowVar an enabled guard wiring on `nodeId` writes — the flat
 * status var always, plus (pii_redact only, for now) one per-category var
 * per configured category. Disabled wirings write nothing (writeTextGuardVars
 * / writeFileGuardVar are never called for a guard that didn't run).
 */
export function guardWriteVarsForNode(nodeId: string, wirings: MiddlewareWiring[]): GuardWriteVar[] {
  const nodeWirings = wirings.filter(w => w.node_id === nodeId && w.enabled);
  const out: GuardWriteVar[] = [];

  for (const w of nodeWirings) {
    const shortLabel = GUARD_SHORT_LABEL[w.def_slug] ?? w.def_slug;
    const varSegment = w.def_slug === 'file-guard' ? FILE_GUARD_VAR_SEGMENT : w.def_slug;
    const guardLabel = GUARD_LABEL_FULL[w.def_slug] ?? w.def_slug;

    out.push({
      flatVar: `${nodeId}_${varSegment}_status`,
      displayRef: `${shortLabel}.status`,
      guardLabel,
    });

    if (w.def_slug === 'pii_redact') {
      for (const category of piiCategoriesFor(w)) {
        out.push({
          flatVar: `${nodeId}_${varSegment}_${category}_status`,
          displayRef: `${shortLabel}.${category}.status`,
          guardLabel,
          detail: `category: ${category}`,
        });
      }
    }
  }

  return out;
}

// Kept separate from useInlinePortWiring/appFlowVars's GUARD_LABELS import
// to avoid a cross-panel dependency for 3 strings — same 3 keys as
// AgentGuardsSection.tsx's GUARD_LABELS, duplicated deliberately (that
// module is guard-config-form-shaped, this one is var-naming-shaped).
const GUARD_LABEL_FULL: Record<string, string> = {
  'file-guard': 'File Guard',
  pii_redact: 'PII Guard',
  prompt_inject: 'Prompt-Injection Guard',
};
