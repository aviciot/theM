/**
 * Tests for graphWalk.ts — reachablePredecessors/reachableSuccessors, shared
 * by the agent builder (via nodeVars.ts's re-export) and AppFlow
 * (docs/APPFLOW_NAMED_PORTS_PLAN.md Phase 2).
 *
 * Run with: node src/lib/__tests__/graphWalk.test.js
 *
 * Inlines the pure functions from graphWalk.ts (no TypeScript runtime
 * needed). When a test runner (vitest/jest) is added, migrate to importing
 * from graphWalk.ts directly.
 */

'use strict';
const assert = require('assert/strict');

function reachablePredecessors(targetId, edges) {
  const pred = new Set();
  const queue = [targetId];
  while (queue.length > 0) {
    const cur = queue.shift();
    for (const e of edges) {
      if (e.target === cur && !pred.has(e.source)) {
        pred.add(e.source);
        queue.push(e.source);
      }
    }
  }
  return pred;
}

function reachableSuccessors(sourceId, edges) {
  const succ = new Set();
  const queue = [sourceId];
  while (queue.length > 0) {
    const cur = queue.shift();
    for (const e of edges) {
      if (e.source === cur && !succ.has(e.target)) {
        succ.add(e.target);
        queue.push(e.target);
      }
    }
  }
  return succ;
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

function edge(source, target) {
  return { id: `${source}->${target}`, source, target };
}

// Graph: A → B → C → D, plus an isolated fork E → F (not connected to A-D)
const edges = [edge('A', 'B'), edge('B', 'C'), edge('C', 'D'), edge('E', 'F')];

console.log('\nreachablePredecessors:');

test('walks backward through multiple hops', () => {
  const pred = reachablePredecessors('D', edges);
  assert.ok(pred.has('A'));
  assert.ok(pred.has('B'));
  assert.ok(pred.has('C'));
  assert.ok(!pred.has('D'), 'should not include the target itself');
});

test('does not cross into an unconnected subgraph', () => {
  const pred = reachablePredecessors('D', edges);
  assert.ok(!pred.has('E'));
  assert.ok(!pred.has('F'));
});

test('returns an empty set for a node with no incoming edges', () => {
  const pred = reachablePredecessors('A', edges);
  assert.equal(pred.size, 0);
});

test('returns an empty set for a node not present in any edge', () => {
  const pred = reachablePredecessors('Z', edges);
  assert.equal(pred.size, 0);
});

console.log('\nreachableSuccessors:');

test('walks forward through multiple hops', () => {
  const succ = reachableSuccessors('A', edges);
  assert.ok(succ.has('B'));
  assert.ok(succ.has('C'));
  assert.ok(succ.has('D'));
  assert.ok(!succ.has('A'), 'should not include the source itself');
});

test('does not cross into an unconnected subgraph', () => {
  const succ = reachableSuccessors('A', edges);
  assert.ok(!succ.has('E'));
  assert.ok(!succ.has('F'));
});

test('returns an empty set for a node with no outgoing edges', () => {
  const succ = reachableSuccessors('D', edges);
  assert.equal(succ.size, 0);
});

test('handles a diamond (two paths to the same node) without infinite loop or duplicates', () => {
  // A → B → D, A → C → D
  const diamond = [edge('A', 'B'), edge('A', 'C'), edge('B', 'D'), edge('C', 'D')];
  const succ = reachableSuccessors('A', diamond);
  assert.deepEqual([...succ].sort(), ['B', 'C', 'D']);
  const pred = reachablePredecessors('D', diamond);
  assert.deepEqual([...pred].sort(), ['A', 'B', 'C']);
});

console.log(`\n${passed + failed} tests: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
