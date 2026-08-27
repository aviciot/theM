/**
 * Tests for nodeVars static analysis.
 *
 * Run with: node src/app/admin/agents/builder/__tests__/nodeVars.test.js
 *
 * Inlines the pure functions from nodeVars.ts (no TypeScript runtime needed).
 * When a test runner (vitest/jest) is added, migrate to importing from nodeVars.ts.
 */

'use strict';
const assert = require('assert/strict');

// ── Inline pure functions from nodeVars.ts ────────────────────────────────────

function extractTemplateVars(tmpl) {
  const matches = [];
  const re = /\{\{\.?(\w+)\}\}/g;
  let m;
  while ((m = re.exec(tmpl)) !== null) matches.push(m[1]);
  return [...new Set(matches)];
}

function extractNodeVars(node) {
  const d = node.data ?? {};
  const cfg = d.config ?? {};
  const st = d.step_type;

  if (st === 'input') {
    const bindVar = (cfg.bindings)?.text || 'input';
    return { reads: [], writes: [bindVar] };
  }

  if (st === 'llm') {
    const userPrompt = cfg.user_prompt || '';
    const systemPrompt = cfg.system_prompt || '';
    const outVar = cfg.output_var || 'output';
    const reads = [...new Set([
      ...extractTemplateVars(userPrompt),
      ...extractTemplateVars(systemPrompt),
      ...(userPrompt === '' ? ['input'] : []),
    ])];
    return { reads, writes: [outVar] };
  }

  if (st === 'transform') {
    const functions = cfg.functions ?? [];
    const reads = [];
    const writes = [];
    for (const f of functions) {
      if (f.input_var) reads.push(f.input_var);
      if (f.output_var) writes.push(f.output_var);
      if (f.fn === 'template' && f.args?.template) {
        reads.push(...extractTemplateVars(String(f.args.template)));
      }
    }
    return { reads: [...new Set(reads)], writes: [...new Set(writes)] };
  }

  if (st === 'http') {
    const urlTemplate = cfg.url_template || '';
    const bodyTemplate = cfg.body_template || '';
    const reads = [...new Set([
      ...extractTemplateVars(urlTemplate),
      ...extractTemplateVars(bodyTemplate),
    ])];
    const extractions = cfg.extractions ?? [];
    const writes = ['http_response', ...extractions.map(e => e.var).filter(Boolean)];
    return { reads, writes: [...new Set(writes)] };
  }

  if (st === 'response') {
    const fromVar = cfg.from_var || 'output';
    return { reads: [fromVar], writes: [] };
  }

  if (st === 'branch') {
    const expr = cfg.expression || '';
    return { reads: extractTemplateVars(expr), writes: [] };
  }

  return { reads: [], writes: [] };
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

function upstreamVarSources(nodeId, allNodes, edges) {
  const predIds = reachablePredecessors(nodeId, edges);
  const result = new Map();
  for (const n of allNodes) {
    if (!predIds.has(n.id)) continue;
    const d = n.data ?? {};
    const { writes } = extractNodeVars(n);
    for (const v of writes) {
      result.set(v, { label: d.label || n.id, step_type: d.step_type });
    }
  }
  return result;
}

function downstreamReadVars(nodeId, allNodes, edges) {
  const succIds = reachableSuccessors(nodeId, edges);
  const result = new Set();
  for (const n of allNodes) {
    if (!succIds.has(n.id)) continue;
    const { reads } = extractNodeVars(n);
    for (const v of reads) result.add(v);
  }
  return result;
}

function edgeRelevantVars(sourceNode, targetNode) {
  const { writes } = extractNodeVars(sourceNode);
  const { reads } = extractNodeVars(targetNode);
  if (reads.length === 0 || writes.length === 0) return [];
  return writes.filter(v => reads.includes(v));
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

function node(id, step_type, config = {}, extra = {}) {
  return { id, data: { step_type, config, label: id, ...extra }, type: 'step' };
}

function edge(source, target) {
  return { id: `${source}->${target}`, source, target };
}

// ── extractNodeVars ───────────────────────────────────────────────────────────

console.log('\nextractNodeVars:');

test('input: writes bound var', () => {
  const n = node('a', 'input', { bindings: { text: 'user_query' } });
  const { reads, writes } = extractNodeVars(n);
  assert.deepEqual(reads, []);
  assert.deepEqual(writes, ['user_query']);
});

test('input: defaults to "input" when no binding', () => {
  const n = node('a', 'input', {});
  assert.deepEqual(extractNodeVars(n).writes, ['input']);
});

test('llm: reads template vars from user_prompt and system_prompt', () => {
  const n = node('a', 'llm', {
    user_prompt: '{{.city}} {{.country}}',
    system_prompt: 'You are {{.persona}}',
    output_var: 'summary',
  });
  const { reads, writes } = extractNodeVars(n);
  assert.ok(reads.includes('city'));
  assert.ok(reads.includes('country'));
  assert.ok(reads.includes('persona'));
  assert.deepEqual(writes, ['summary']);
});

test('llm: adds "input" fallback read when user_prompt is empty', () => {
  const n = node('a', 'llm', { user_prompt: '', output_var: 'out' });
  assert.ok(extractNodeVars(n).reads.includes('input'));
});

test('llm: no "input" fallback when user_prompt is set', () => {
  const n = node('a', 'llm', { user_prompt: '{{.foo}}', output_var: 'out' });
  assert.ok(!extractNodeVars(n).reads.includes('input'));
});

test('llm: defaults output_var to "output"', () => {
  const n = node('a', 'llm', {});
  assert.deepEqual(extractNodeVars(n).writes, ['output']);
});

test('transform: reads input_var and writes output_var per function', () => {
  const n = node('a', 'transform', {
    functions: [
      { fn: 'strip_fences', input_var: 'raw', output_var: 'clean' },
      { fn: 'json_path', input_var: 'clean', output_var: 'city', args: { path: '$.city' } },
    ],
  });
  const { reads, writes } = extractNodeVars(n);
  assert.deepEqual(reads, ['raw', 'clean']);
  assert.deepEqual(writes, ['clean', 'city']);
});

test('transform: deduplicates reads (same input_var used by multiple fns)', () => {
  const n = node('a', 'transform', {
    functions: [
      { fn: 'json_path', input_var: 'prefs_json', output_var: 'city1', args: { path: '$.city1' } },
      { fn: 'json_path', input_var: 'prefs_json', output_var: 'city2', args: { path: '$.city2' } },
    ],
  });
  assert.deepEqual(extractNodeVars(n).reads, ['prefs_json']);
});

test('transform: does NOT include legacy expressions/extractions (not in Go runtime)', () => {
  const n = node('a', 'transform', {
    functions: [{ fn: 'upper', input_var: 'x', output_var: 'y' }],
    expressions: { z: '{{.w}}' },    // dead frontend-only field
    extractions: [{ from_var: 'a', var: 'b' }],  // dead frontend-only field
  });
  const { reads, writes } = extractNodeVars(n);
  assert.ok(!reads.includes('w'), 'expressions should be ignored');
  assert.ok(!reads.includes('a'), 'transform extractions should be ignored');
  assert.ok(!writes.includes('z'), 'expressions outputs should be ignored');
  assert.ok(!writes.includes('b'), 'transform extractions outputs should be ignored');
});

test('http: reads template vars from url_template and body_template', () => {
  const n = node('a', 'http', {
    url_template: 'https://api.example.com?lat={{.city_lat}}&lon={{.city_lon}}',
    body_template: '{"q":"{{.query}}"}',
  });
  const { reads } = extractNodeVars(n);
  assert.ok(reads.includes('city_lat'));
  assert.ok(reads.includes('city_lon'));
  assert.ok(reads.includes('query'));
});

test('http: always writes http_response', () => {
  const n = node('a', 'http', { url_template: 'https://api.example.com' });
  assert.ok(extractNodeVars(n).writes.includes('http_response'));
});

test('http: writes extraction vars alongside http_response', () => {
  const n = node('a', 'http', {
    url_template: 'https://api.example.com',
    extractions: [
      { var: 'token', json_path: '$.access_token' },
      { var: 'expires', json_path: '$.expires_in' },
    ],
  });
  const { writes } = extractNodeVars(n);
  assert.ok(writes.includes('http_response'));
  assert.ok(writes.includes('token'));
  assert.ok(writes.includes('expires'));
});

test('response: reads from_var', () => {
  const n = node('a', 'response', { from_var: 'recommendation' });
  assert.deepEqual(extractNodeVars(n).reads, ['recommendation']);
  assert.deepEqual(extractNodeVars(n).writes, []);
});

test('branch: reads template vars from expression', () => {
  const n = node('a', 'branch', { expression: '{{.recommendation}}' });
  assert.deepEqual(extractNodeVars(n).reads, ['recommendation']);
});

// ── edgeRelevantVars ──────────────────────────────────────────────────────────

console.log('\nedgeRelevantVars:');

test('returns intersection of source writes and target reads', () => {
  const src = node('s', 'transform', {
    functions: [
      { fn: 'json_path', input_var: 'prefs_json', output_var: 'city1_lat', args: { path: '$.city1_lat' } },
      { fn: 'json_path', input_var: 'prefs_json', output_var: 'city1_lon', args: { path: '$.city1_lon' } },
      { fn: 'json_path', input_var: 'prefs_json', output_var: 'budget', args: { path: '$.budget' } },
    ],
  });
  const tgt = node('t', 'http', {
    url_template: 'https://api.example.com?lat={{.city1_lat}}&lon={{.city1_lon}}',
  });
  const result = edgeRelevantVars(src, tgt);
  assert.deepEqual(result.sort(), ['city1_lat', 'city1_lon']);
  assert.ok(!result.includes('budget'), 'should not include vars target does not read');
});

test('returns [] when no intersection (not all source writes)', () => {
  const src = node('s', 'transform', {
    functions: [{ fn: 'upper', input_var: 'x', output_var: 'y' }],
  });
  const tgt = node('t', 'http', {
    url_template: 'https://api.example.com?foo={{.completely_different}}',
  });
  assert.deepEqual(edgeRelevantVars(src, tgt), []);
});

test('returns [] when target has no reads (e.g. branch with no template vars)', () => {
  const src = node('s', 'llm', { output_var: 'recommendation' });
  const tgt = node('t', 'response', { from_var: 'recommendation' });
  const result = edgeRelevantVars(src, tgt);
  assert.deepEqual(result, ['recommendation']);
});

test('returns [] when source has no writes', () => {
  const src = node('s', 'response', { from_var: 'x' });
  const tgt = node('t', 'branch', { expression: '{{.x}}' });
  assert.deepEqual(edgeRelevantVars(src, tgt), []);
});

// ── graph-aware: reachablePredecessors / reachableSuccessors ─────────────────

console.log('\nGraph traversal:');

// Graph: A → B → C → D
const gNodes = [
  node('A', 'input', { bindings: { text: 'user_req' } }),
  node('B', 'llm', { user_prompt: '{{.user_req}}', output_var: 'prefs_json' }),
  node('C', 'transform', { functions: [{ fn: 'json_path', input_var: 'prefs_json', output_var: 'city' }] }),
  node('D', 'http', { url_template: 'https://api.example.com/{{.city}}' }),
];
const gEdges = [edge('A', 'B'), edge('B', 'C'), edge('C', 'D')];

test('reachablePredecessors of D includes A, B, C', () => {
  const pred = reachablePredecessors('D', gEdges);
  assert.ok(pred.has('A'));
  assert.ok(pred.has('B'));
  assert.ok(pred.has('C'));
  assert.ok(!pred.has('D'));
});

test('reachableSuccessors of A includes B, C, D', () => {
  const succ = reachableSuccessors('A', gEdges);
  assert.ok(succ.has('B'));
  assert.ok(succ.has('C'));
  assert.ok(succ.has('D'));
  assert.ok(!succ.has('A'));
});

// ── upstreamVarSources ────────────────────────────────────────────────────────

console.log('\nupstreamVarSources:');

test('resolves var written several hops upstream (not just direct edge)', () => {
  // D reads "city" written by C, but also "user_req" written by A (2 hops away)
  // This verifies graph-aware resolution, not direct-edge-only.
  const map = upstreamVarSources('D', gNodes, gEdges);
  assert.ok(map.has('city'), 'city written by C should be visible to D');
  assert.ok(map.has('user_req'), 'user_req written by A should be visible to D (2 hops)');
  assert.ok(map.has('prefs_json'), 'prefs_json written by B should be visible to D');
});

test('does not include vars written by unreachable nodes', () => {
  const isolated = node('X', 'llm', { output_var: 'secret' });
  const map = upstreamVarSources('D', [...gNodes, isolated], gEdges);
  assert.ok(!map.has('secret'));
});

// ── downstreamReadVars ────────────────────────────────────────────────────────

console.log('\ndownstreamReadVars:');

test('collects reads from all reachable successors', () => {
  // From A, successors are B, C, D. Their reads: user_req, prefs_json, city
  const reads = downstreamReadVars('A', gNodes, gEdges);
  assert.ok(reads.has('user_req'));   // B reads it
  assert.ok(reads.has('prefs_json')); // C reads it
  assert.ok(reads.has('city'));       // D reads it
});

test('node with no successors returns empty set', () => {
  const reads = downstreamReadVars('D', gNodes, gEdges);
  assert.equal(reads.size, 0);
});

// ── Issue 5: missing detection without direct incoming edge ───────────────────

console.log('\nMissing var detection:');

test('var written far upstream is NOT marked unresolved for a node with no direct incoming edge', () => {
  // E reads "prefs_json" written by B; E is connected D→E (D does not write prefs_json).
  // Graph-aware lookup must still find B as the writer via predecessor walk.
  const eNode = node('E', 'http', { url_template: 'https://x.com?q={{.prefs_json}}' });
  const nodes2 = [...gNodes, eNode];
  const edges2 = [...gEdges, edge('D', 'E')];
  const map = upstreamVarSources('E', nodes2, edges2);
  assert.ok(map.has('prefs_json'), 'prefs_json should resolve via graph walk, not just direct edge');
});

test('var with no reachable upstream writer is correctly absent from map', () => {
  // F reads "nonexistent_var" that nothing in the graph writes
  const fNode = node('F', 'http', { url_template: 'https://x.com?q={{.nonexistent_var}}' });
  const nodes2 = [...gNodes, fNode];
  const edges2 = [...gEdges, edge('D', 'F')];
  const map = upstreamVarSources('F', nodes2, edges2);
  assert.ok(!map.has('nonexistent_var'));
});

// ── Summary ───────────────────────────────────────────────────────────────────

console.log(`\n${passed + failed} tests: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
