/**
 * Tests for templateVars.ts — extractTemplateVars, shared by the agent
 * builder (via nodeVars.ts's re-export) and AppFlow
 * (docs/APPFLOW_NAMED_PORTS_PLAN.md Phase 2).
 *
 * Run with: node src/lib/__tests__/templateVars.test.js
 *
 * Inlines the pure function from templateVars.ts (no TypeScript runtime
 * needed). When a test runner (vitest/jest) is added, migrate to importing
 * from templateVars.ts directly.
 */

'use strict';
const assert = require('assert/strict');

function extractTemplateVars(tmpl) {
  const matches = [];
  const re = /\{\{\.?(\w+)\}\}/g;
  let m;
  while ((m = re.exec(tmpl)) !== null) matches.push(m[1]);
  return [...new Set(matches)];
}

let passed = 0;
let failed = 0;

function test(name, fn) {
  try {
    fn();
    console.log(`  ✓ ${name}`);
    passed++;
  } catch (e) {
    console.error(`  ✗ ${name}`);
    console.error(`    ${e.message}`);
    failed++;
  }
}

console.log('\nextractTemplateVars:');

test('extracts a single dotted var', () => {
  assert.deepEqual(extractTemplateVars('{{.city}}'), ['city']);
});

test('extracts a var without the leading dot', () => {
  assert.deepEqual(extractTemplateVars('{{city}}'), ['city']);
});

test('extracts multiple distinct vars in order of first appearance', () => {
  assert.deepEqual(extractTemplateVars('{{.city}} and {{.country}}'), ['city', 'country']);
});

test('deduplicates repeated references to the same var', () => {
  assert.deepEqual(extractTemplateVars('{{.x}} {{.x}} {{.x}}'), ['x']);
});

test('returns [] for a string with no template vars', () => {
  assert.deepEqual(extractTemplateVars('plain text, no vars'), []);
});

test('returns [] for an empty string', () => {
  assert.deepEqual(extractTemplateVars(''), []);
});

test('ignores non-word characters inside braces (does not partially match)', () => {
  assert.deepEqual(extractTemplateVars('{{.sentiment}} vs {{ .other }}'), ['sentiment']);
});

console.log(`\n${passed + failed} tests: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
