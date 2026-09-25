/**
 * Tests for validateConnection's data-port rejection guard
 * (docs/APPFLOW_NAMED_PORTS_PLAN.md Phase 4).
 *
 * Run with: node src/app/admin/applications/components/__tests__/validateConnection.test.js
 *
 * Inlines the pure function from CanvasInner.tsx (no TypeScript/JSX runtime
 * needed). When a test runner (vitest/jest) is added, migrate to importing
 * directly.
 */

'use strict';
const assert = require('assert/strict');

const NODE_PORTS = {
  entryPoint:   { accepts: [],                              emits: ['request'] },
  orchestrator: { accepts: ['request', 'signal', 'fc_out'],  emits: ['task', 'signal', 'fc_in'] },
  agent:        { accepts: ['task', 'mw_task', 'fc_out'],    emits: ['result'] },
  middleware:   { accepts: ['task', 'mw_task'],               emits: ['mw_task'] },
  flowControl:  { accepts: ['request', 'task', 'signal', 'fc_in', 'fc_out', 'result'], emits: ['fc_out', 'fc_in', 'request', 'task'] },
  inline:       { accepts: ['request', 'task', 'signal', 'fc_in', 'fc_out', 'result'], emits: ['fc_out', 'fc_in', 'request', 'task'] },
};

function validateConnection(sourceType, targetType, sourceId, targetId, edges, sourceHandle, targetHandle) {
  if (sourceHandle?.startsWith('data-') || targetHandle?.startsWith('data-')) {
    return `Named data port wiring isn't available yet`;
  }

  const src = NODE_PORTS[sourceType];
  const tgt = NODE_PORTS[targetType];
  if (!src || !tgt) return `Unknown node type`;

  const compatible = src.emits.some(sig => tgt.accepts.includes(sig));
  if (!compatible) return `Cannot connect ${sourceType} → ${targetType}`;

  if (edges.some(e => e.source === sourceId && e.target === targetId && (e.sourceHandle ?? null) === (sourceHandle ?? null))) {
    return `These nodes are already connected`;
  }

  if (src.maxOutgoing !== undefined) {
    const out = edges.filter(e => e.source === sourceId).length;
    if (out >= src.maxOutgoing) return `Entry point already has an orchestrator — remove it first`;
  }

  if (tgt.maxIncoming !== undefined) {
    const inc = edges.filter(e => e.target === targetId).length;
    if (inc >= tgt.maxIncoming) return `This node already has the maximum number of incoming connections`;
  }

  return null;
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

console.log('\nvalidateConnection — data port rejection (Phase 4):');

test('rejects a connection FROM a data-out-* source handle', () => {
  const err = validateConnection('inline', 'inline', 'a', 'b', [], 'data-out-output', null);
  assert.ok(err, 'expected a rejection message');
});

test('rejects a connection TO a data-in-* target handle', () => {
  const err = validateConnection('inline', 'inline', 'a', 'b', [], null, 'data-in-foo');
  assert.ok(err, 'expected a rejection message');
});

test('rejects when BOTH ends are data handles', () => {
  const err = validateConnection('inline', 'inline', 'a', 'b', [], 'data-out-output', 'data-in-foo');
  assert.ok(err);
});

test('still allows a plain control connection between two inline nodes (no handles)', () => {
  const err = validateConnection('inline', 'inline', 'a', 'b', [], null, null);
  assert.equal(err, null);
});

test('still allows a plain control-out handle (ctrl-out-true) to connect normally', () => {
  const err = validateConnection('inline', 'inline', 'a', 'b', [], 'ctrl-out-true', null);
  assert.equal(err, null);
});

test('still rejects incompatible node types the same as before (unaffected by the new guard)', () => {
  const err = validateConnection('entryPoint', 'entryPoint', 'a', 'b', [], null, null);
  assert.ok(err);
});

console.log(`\n${passed + failed} tests: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
