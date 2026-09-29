/**
 * Tests for guardWriteVars.ts — guardWriteVarsForNode.
 *
 * Run with: node src/app/admin/applications/components/cbv/__tests__/guardWriteVars.test.js
 *
 * Inlines the pure logic from guardWriteVars.ts (no TypeScript runtime
 * needed). Naming convention MUST stay byte-for-byte identical to
 * go/internal/appflow/workflow.go's guardStatusVarName/writeGuardCategoryVars/
 * writeFileGuardVar (docs/APPFLOW_GUARD_OUTPUT_PORTS_PLAN.md Phase 1/1.5/2) —
 * that's the whole point of this module, so these tests pin the exact
 * strings, not just "some var got produced."
 */

'use strict';
const assert = require('assert/strict');

const FILE_GUARD_VAR_SEGMENT = 'file_guard';

const GUARD_SHORT_LABEL = {
  'file-guard': 'file_guard',
  pii_redact: 'pii_guard',
  prompt_inject: 'prompt_guard',
};

const GUARD_LABEL_FULL = {
  'file-guard': 'File Guard',
  pii_redact: 'PII Guard',
  prompt_inject: 'Prompt-Injection Guard',
};

const ALL_PII_CATEGORIES = ['email', 'phone', 'credit_card', 'ssn'];

function piiCategoriesFor(wiring) {
  const configured = wiring.config_override.categories;
  if (Array.isArray(configured) && configured.length > 0) {
    return configured.filter(c => typeof c === 'string');
  }
  return ALL_PII_CATEGORIES;
}

function guardWriteVarsForNode(nodeId, wirings) {
  const nodeWirings = wirings.filter(w => w.node_id === nodeId && w.enabled);
  const out = [];

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

function wiring(overrides) {
  return {
    id: 'w1', application_id: 'app-1', def_id: 'def-1', position: 0,
    config_override: {}, enabled: true, node_id: 'agent1',
    created_at: '', updated_at: '', def_slug: 'pii_redact',
    ...overrides,
  };
}

let passed = 0, failed = 0;
function test(name, fn) {
  try { fn(); passed++; console.log(`  ✓ ${name}`); }
  catch (e) { failed++; console.log(`  ✗ ${name}\n    ${e.message}`); }
}

console.log('guardWriteVarsForNode:');

test('pii_redact with no categories configured expands to the flat status + all 4 category vars', () => {
  const vars = guardWriteVarsForNode('agent1', [wiring({})]);
  const flatVars = vars.map(v => v.flatVar);
  assert.deepEqual(flatVars, [
    'agent1_pii_redact_status',
    'agent1_pii_redact_email_status',
    'agent1_pii_redact_phone_status',
    'agent1_pii_redact_credit_card_status',
    'agent1_pii_redact_ssn_status',
  ]);
});

test('pii_redact restricted to specific categories only expands those', () => {
  const vars = guardWriteVarsForNode('agent1', [
    wiring({ config_override: { categories: ['email', 'ssn'] } }),
  ]);
  const flatVars = vars.map(v => v.flatVar);
  assert.deepEqual(flatVars, [
    'agent1_pii_redact_status',
    'agent1_pii_redact_email_status',
    'agent1_pii_redact_ssn_status',
  ]);
});

test('display ref uses the short dotted form, not the flat key', () => {
  const vars = guardWriteVarsForNode('agent1', [
    wiring({ config_override: { categories: ['email'] } }),
  ]);
  assert.equal(vars[0].displayRef, 'pii_guard.status');
  assert.equal(vars[1].displayRef, 'pii_guard.email.status');
});

test('file-guard uses "file_guard" (underscore) as its var segment despite the def_slug being "file-guard" (hyphen)', () => {
  const vars = guardWriteVarsForNode('agent1', [
    wiring({ def_slug: 'file-guard' }),
  ]);
  assert.deepEqual(vars.map(v => v.flatVar), ['agent1_file_guard_status']);
  assert.equal(vars[0].displayRef, 'file_guard.status');
});

test('prompt_inject writes only its flat status var — no per-category expansion', () => {
  const vars = guardWriteVarsForNode('agent1', [
    wiring({ def_slug: 'prompt_inject' }),
  ]);
  assert.deepEqual(vars.map(v => v.flatVar), ['agent1_prompt_inject_status']);
});

test('a disabled wiring contributes no vars at all', () => {
  const vars = guardWriteVarsForNode('agent1', [
    wiring({ enabled: false }),
  ]);
  assert.deepEqual(vars, []);
});

test('a wiring on a different node_id is excluded', () => {
  const vars = guardWriteVarsForNode('agent1', [
    wiring({ node_id: 'agent2' }),
  ]);
  assert.deepEqual(vars, []);
});

test('two different guards on the same node both contribute, independently', () => {
  const vars = guardWriteVarsForNode('agent1', [
    wiring({ def_slug: 'pii_redact', config_override: { categories: ['email'] } }),
    wiring({ id: 'w2', def_slug: 'prompt_inject' }),
  ]);
  const flatVars = vars.map(v => v.flatVar);
  assert.deepEqual(flatVars, [
    'agent1_pii_redact_status',
    'agent1_pii_redact_email_status',
    'agent1_prompt_inject_status',
  ]);
});

console.log(`\n${passed + failed} tests: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
