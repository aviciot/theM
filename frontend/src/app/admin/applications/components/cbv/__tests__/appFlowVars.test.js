/**
 * Tests for appFlowVars.ts — extractInlineNodeVars / upstreamAppFlowVarSources.
 *
 * Run with: node src/app/admin/applications/components/cbv/__tests__/appFlowVars.test.js
 *
 * Inlines the pure functions from appFlowVars.ts (no TypeScript runtime
 * needed) plus their two dependencies from src/lib/. When a test runner
 * (vitest/jest) is added, migrate to importing directly.
 *
 * resolveBinding (this file's replacement for an earlier resolveAlias) is
 * tested in useInlinePortWiring.test.js instead, since it's tightly coupled
 * to that module's binding-commit shape.
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

function extractInlineNodeVars(node) {
  const d = node.data ?? {};
  const cfg = d.config ?? {};

  if (d.node_type === 'llm') {
    const userPrompt = cfg.user_prompt || '';
    const systemPrompt = cfg.system_prompt || '';
    const outputVar = cfg.output_var || 'output';
    const reads = [...new Set([
      ...extractTemplateVars(userPrompt),
      ...extractTemplateVars(systemPrompt),
    ])];
    return { reads, writes: [outputVar] };
  }

  if (d.node_type === 'condition') {
    const expression = cfg.expression || '';
    return { reads: extractTemplateVars(expression), writes: [] };
  }

  return { reads: [], writes: [] };
}

function upstreamAppFlowVarSources(nodeId, allNodes, edges) {
  const predIds = reachablePredecessors(nodeId, edges);
  const result = new Map();
  for (const n of allNodes) {
    if (!predIds.has(n.id)) continue;
    const d = n.data ?? {};
    const { writes } = extractInlineNodeVars(n);
    for (const v of writes) {
      result.set(v, { label: d.display_name || n.id, node_type: d.node_type });
    }
  }
  return result;
}

// ── Test helpers ─────────────────────────────────────────────────────────────

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

function node(id, node_type, config = {}, display_name) {
  return { id, data: { node_type, config, display_name: display_name ?? id } };
}

function edge(source, target) {
  return { id: `${source}->${target}`, source, target };
}

// ── extractInlineNodeVars ─────────────────────────────────────────────────────

console.log('\nextractInlineNodeVars:');

test('llm: reads template vars from user_prompt and system_prompt', () => {
  const n = node('a', 'llm', {
    user_prompt: '{{.city}}',
    system_prompt: 'You are {{.persona}}',
    output_var: 'summary',
  });
  const { reads, writes } = extractInlineNodeVars(n);
  assert.ok(reads.includes('city'));
  assert.ok(reads.includes('persona'));
  assert.deepEqual(writes, ['summary']);
});

test('llm: defaults output_var to "output"', () => {
  const n = node('a', 'llm', {});
  assert.deepEqual(extractInlineNodeVars(n).writes, ['output']);
});

test('llm: does NOT add an "input" fallback read (unlike agentgen)', () => {
  // AppFlow's llm case has no vars["input"] fallback semantics like agentgen's
  // execLLM — an empty user_prompt just means zero reads from it.
  const n = node('a', 'llm', { user_prompt: '', output_var: 'out' });
  assert.deepEqual(extractInlineNodeVars(n).reads, []);
});

test('condition: reads simple {{.var}} references from expression, writes nothing', () => {
  // The shared extractTemplateVars regex only matches simple {{.var}} refs,
  // not Go template function calls like {{eq .sentiment "x"}} — confirmed in
  // src/lib/__tests__/templateVars.test.js. Use a matching expression here.
  const n = node('a', 'condition', { expression: '{{.sentiment}}' });
  const { reads, writes } = extractInlineNodeVars(n);
  assert.deepEqual(reads, ['sentiment']);
  assert.deepEqual(writes, []);
});

test('condition: empty expression reads nothing', () => {
  const n = node('a', 'condition', {});
  assert.deepEqual(extractInlineNodeVars(n).reads, []);
});

test('other kinds (router/hil/fork/join) never read or write FlowVars', () => {
  for (const kind of ['router', 'hil', 'fork', 'join', 'agent', 'orchestrator']) {
    const n = node('a', kind, { expression: '{{.would_be_ignored}}', output_var: 'ignored' });
    const { reads, writes } = extractInlineNodeVars(n);
    assert.deepEqual(reads, [], `${kind} should have no reads`);
    assert.deepEqual(writes, [], `${kind} should have no writes`);
  }
});

// ── upstreamAppFlowVarSources ─────────────────────────────────────────────────

console.log('\nupstreamAppFlowVarSources:');

// Graph: A(llm, writes "prefs") → B(condition) → C(llm, writes "summary") → D(condition)
const gNodes = [
  node('A', 'llm', { output_var: 'prefs' }, 'Extract Prefs'),
  node('B', 'condition', { expression: '{{.prefs}}' }),
  node('C', 'llm', { user_prompt: '{{.prefs}}', output_var: 'summary' }, 'Summarize'),
  node('D', 'condition', { expression: '{{.summary}}' }),
];
const gEdges = [edge('A', 'B'), edge('B', 'C'), edge('C', 'D')];

test('resolves a var written several hops upstream, not just a direct edge', () => {
  const map = upstreamAppFlowVarSources('D', gNodes, gEdges);
  assert.ok(map.has('summary'), 'summary written by C (direct)');
  assert.ok(map.has('prefs'), 'prefs written by A (2 hops away)');
});

test('resolved entry carries the source node label and node_type', () => {
  const map = upstreamAppFlowVarSources('D', gNodes, gEdges);
  const src = map.get('prefs');
  assert.equal(src.label, 'Extract Prefs');
  assert.equal(src.node_type, 'llm');
});

test('a condition node contributes nothing to the upstream var map (writes nothing)', () => {
  const map = upstreamAppFlowVarSources('D', gNodes, gEdges);
  // B is a condition node in the upstream path; it must never appear as a source.
  for (const src of map.values()) {
    assert.notEqual(src.node_type, 'condition');
  }
});

test('does not include vars written by unreachable nodes', () => {
  const isolated = node('X', 'llm', { output_var: 'secret' });
  const map = upstreamAppFlowVarSources('D', [...gNodes, isolated], gEdges);
  assert.ok(!map.has('secret'));
});

test('var with no reachable upstream writer is correctly absent from the map', () => {
  const map = upstreamAppFlowVarSources('D', gNodes, gEdges);
  assert.ok(!map.has('nonexistent_var'));
});

// (resolveBinding — the ID-based alias resolver that replaced resolveAlias —
// is covered in useInlinePortWiring.test.js, alongside the binding-commit
// logic it's paired with.)

// ── Summary ───────────────────────────────────────────────────────────────────

console.log(`\n${passed + failed} tests: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
