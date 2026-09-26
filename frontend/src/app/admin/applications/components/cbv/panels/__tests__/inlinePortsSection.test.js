/**
 * Tests for InlinePortsSection.tsx's `hasDirectEntryPointEdge` logic — the
 * fix for `.input` incorrectly showing as "unresolved" (red) whenever an
 * llm/condition node is wired directly to the canvas's Entry Point.
 *
 * Run with: node src/app/admin/applications/components/cbv/panels/__tests__/inlinePortsSection.test.js
 *
 * `.input` is filled by the Entry Point at the start of every run
 * (go/internal/appflow/workflow.go's `accumulated := input.UserMessage`) —
 * it's never "written" by any node the graph-walk heuristic can find, so
 * without this special case it always renders red even when the wire from
 * the Entry Point is right there on the canvas. Inlines the pure boolean
 * check (no TypeScript/React runtime needed), following the convention
 * established by appFlowVars.test.js.
 */

'use strict';
const assert = require('assert/strict');

function hasDirectEntryPointEdge(thisNodeId, nodes, edges) {
  return edges.some(e =>
    e.target === thisNodeId && nodes.find(n => n.id === e.source)?.type === 'entryPoint'
  );
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

function node(id, type) {
  return { id, type };
}

function edge(source, target) {
  return { id: `${source}->${target}`, source, target };
}

console.log('\nhasDirectEntryPointEdge:');

test('true when the node has a direct incoming edge from an entryPoint-type node', () => {
  const nodes = [node('ep_1', 'entryPoint'), node('llm_1', 'inline')];
  const edges = [edge('ep_1', 'llm_1')];
  assert.equal(hasDirectEntryPointEdge('llm_1', nodes, edges), true);
});

test('false when the incoming edge is from a non-entryPoint node', () => {
  const nodes = [node('llm_0', 'inline'), node('llm_1', 'inline')];
  const edges = [edge('llm_0', 'llm_1')];
  assert.equal(hasDirectEntryPointEdge('llm_1', nodes, edges), false);
});

test('false when there is no incoming edge at all', () => {
  const nodes = [node('llm_1', 'inline')];
  const edges = [];
  assert.equal(hasDirectEntryPointEdge('llm_1', nodes, edges), false);
});

test('false for an indirect (2-hop) entry point connection — only direct counts', () => {
  // ep_1 -> llm_0 -> llm_1: llm_1's OWN incoming edge is from llm_0, not ep_1.
  const nodes = [node('ep_1', 'entryPoint'), node('llm_0', 'inline'), node('llm_1', 'inline')];
  const edges = [edge('ep_1', 'llm_0'), edge('llm_0', 'llm_1')];
  assert.equal(hasDirectEntryPointEdge('llm_1', nodes, edges), false);
});

console.log(`\n${passed + failed} tests: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
