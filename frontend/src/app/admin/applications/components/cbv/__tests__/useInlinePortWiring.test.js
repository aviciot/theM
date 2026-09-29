/**
 * Tests for useInlinePortWiring.ts — Phase 5 drag-to-connect named data-port
 * wiring (docs/APPFLOW_NAMED_PORTS_PLAN.md).
 *
 * Run with: node src/app/admin/applications/components/cbv/__tests__/useInlinePortWiring.test.js
 *
 * Inlines the pure functions (no TypeScript runtime needed), following the
 * convention established by appFlowVars.test.js.
 *
 * REVISED 2026-09-26: input_aliases now stores {alias: {source_node_id,
 * source_var}} — a reference to the source NODE, not a copy of its
 * output_var's name. See useInlinePortWiring.ts's module doc for the live
 * user feedback that drove this (renames not propagating, same-named
 * outputs being indistinguishable).
 */

'use strict';
const assert = require('assert/strict');

function nameableFields(nodeType) {
  if (nodeType === 'llm') {
    return [
      { key: 'system_prompt', label: 'System Prompt' },
      { key: 'user_prompt', label: 'User Prompt' },
    ];
  }
  if (nodeType === 'condition') {
    return [{ key: 'expression', label: 'Expression' }];
  }
  return [];
}

// ── guardWriteVarsForNode (inlined from guardWriteVars.ts, see its own
// test file for the full behavior — only what resolveDragSource needs) ──

const FILE_GUARD_VAR_SEGMENT = 'file_guard';
const GUARD_SHORT_LABEL = { 'file-guard': 'file_guard', pii_redact: 'pii_guard', prompt_inject: 'prompt_guard' };
const GUARD_LABEL_FULL = { 'file-guard': 'File Guard', pii_redact: 'PII Guard', prompt_inject: 'Prompt-Injection Guard' };
const ALL_PII_CATEGORIES = ['email', 'phone', 'credit_card', 'ssn'];

function piiCategoriesFor(wiring) {
  const configured = wiring.config_override.categories;
  if (Array.isArray(configured) && configured.length > 0) return configured.filter(c => typeof c === 'string');
  return ALL_PII_CATEGORIES;
}

function guardWriteVarsForNode(nodeId, wirings) {
  const nodeWirings = wirings.filter(w => w.node_id === nodeId && w.enabled);
  const out = [];
  for (const w of nodeWirings) {
    const shortLabel = GUARD_SHORT_LABEL[w.def_slug] ?? w.def_slug;
    const varSegment = w.def_slug === 'file-guard' ? FILE_GUARD_VAR_SEGMENT : w.def_slug;
    const guardLabel = GUARD_LABEL_FULL[w.def_slug] ?? w.def_slug;
    out.push({ flatVar: `${nodeId}_${varSegment}_status`, displayRef: `${shortLabel}.status`, guardLabel });
    if (w.def_slug === 'pii_redact') {
      for (const category of piiCategoriesFor(w)) {
        out.push({ flatVar: `${nodeId}_${varSegment}_${category}_status`, displayRef: `${shortLabel}.${category}.status`, guardLabel, detail: `category: ${category}` });
      }
    }
  }
  return out;
}

// ── resolveDragSource / isBindableSource (docs/APPFLOW_GUARD_OUTPUT_PORTS_PLAN.md
// Phase 2 — extends the pre-existing llm-only source resolution to any node
// kind with an enabled guard wiring) ──

function resolveDragSource(node, wirings) {
  if (!node) return [];
  const d = node.data;
  const out = [];
  if (d.node_type === 'llm') {
    const outputVar = d.config?.output_var || 'output';
    out.push({ varName: outputVar, label: outputVar });
  }
  for (const g of guardWriteVarsForNode(node.id, wirings)) {
    out.push({ varName: g.flatVar, label: `${g.guardLabel}: ${g.displayRef}` });
  }
  return out;
}

function isBindableSource(node, wirings) {
  return resolveDragSource(node, wirings).length > 0;
}

function resolveDropTarget(targetNode) {
  if (!targetNode) return { kind: 'none' };
  const nodeType = targetNode.data.node_type;
  const fields = nameableFields(nodeType);
  if (fields.length === 0) return { kind: 'none' };
  if (fields.length === 1) return { kind: 'auto', field: fields[0] };
  return { kind: 'ambiguous', fields };
}

function getInputAliases(node) {
  const cfg = node.data.config ?? {};
  return cfg.input_aliases ?? {};
}

function sanitizeAliasChars(s) {
  // A literal `.` must become `_`: {{.aliasName}} is real Go text/template
  // syntax against a flat map[string]string — a dot inside the name would
  // parse as chained field access, not a literal map key.
  return s.trim().replace(/[\s{}"'`.]+/g, '_');
}

function defaultAliasFor(sourceNode, sourceVar, existing) {
  const label = sourceNode.data.display_name || sourceNode.id;
  const base = sanitizeAliasChars(`${label}_${sourceVar}`);
  if (!(base in existing)) return base;
  let i = 2;
  while (`${base}_${i}` in existing) i++;
  return `${base}_${i}`;
}

function appendTemplateRef(text, alias) {
  const ref = `{{.${alias}}}`;
  const trimmed = text.trim();
  return trimmed ? `${text} ${ref}` : ref;
}

function commitInlinePortBinding(sourceNode, targetNodeId, field, setNodes, sourceVar) {
  const sourceCfg = sourceNode.data.config ?? {};
  const resolvedSourceVar = sourceVar ?? sourceCfg.output_var ?? 'output';

  setNodes(ns => ns.map(n => {
    if (n.id !== targetNodeId) return n;
    const nd = n.data;
    const cfg = nd.config ?? {};
    const existingAliases = cfg.input_aliases ?? {};
    const alias = defaultAliasFor(sourceNode, resolvedSourceVar, existingAliases);
    const currentText = cfg[field.key] || '';
    return {
      ...n,
      data: {
        ...nd,
        config: {
          ...cfg,
          input_aliases: { ...existingAliases, [alias]: { source_node_id: sourceNode.id, source_var: resolvedSourceVar } },
          [field.key]: appendTemplateRef(currentText, alias),
        },
      },
    };
  }));
}

function renameInlinePortAlias(nodeId, oldAlias, newAlias, setNodes) {
  if (oldAlias === newAlias || !newAlias) return;
  setNodes(ns => ns.map(n => {
    if (n.id !== nodeId) return n;
    const nd = n.data;
    const cfg = nd.config ?? {};
    const aliases = getInputAliases(n);
    if (!(oldAlias in aliases)) return n;

    const nextAliases = { ...aliases };
    nextAliases[newAlias] = nextAliases[oldAlias];
    delete nextAliases[oldAlias];

    const nextCfg = { ...cfg, input_aliases: nextAliases };
    const oldRef = new RegExp(`\\{\\{\\.${oldAlias}\\}\\}`, 'g');
    for (const field of nameableFields(nd.node_type)) {
      const text = cfg[field.key];
      if (typeof text === 'string' && text.includes(`{{.${oldAlias}}}`)) {
        nextCfg[field.key] = text.replace(oldRef, `{{.${newAlias}}}`);
      }
    }
    return { ...n, data: { ...nd, config: nextCfg } };
  }));
}

function deleteInlinePortAlias(nodeId, alias, setNodes) {
  setNodes(ns => ns.map(n => {
    if (n.id !== nodeId) return n;
    const nd = n.data;
    const cfg = nd.config ?? {};
    const aliases = getInputAliases(n);
    if (!(alias in aliases)) return n;

    const nextAliases = { ...aliases };
    delete nextAliases[alias];

    const nextCfg = { ...cfg, input_aliases: nextAliases };
    const ref = new RegExp(`\\s?\\{\\{\\.${alias}\\}\\}`, 'g');
    for (const field of nameableFields(nd.node_type)) {
      const text = cfg[field.key];
      if (typeof text === 'string' && text.includes(`{{.${alias}}}`)) {
        nextCfg[field.key] = text.replace(ref, '');
      }
    }
    return { ...n, data: { ...nd, config: nextCfg } };
  }));
}

function resolveBinding(node, varName, allNodes, wirings = []) {
  const aliases = getInputAliases(node);
  const binding = aliases[varName];
  if (!binding) return null;
  const sourceNode = allNodes.find(n => n.id === binding.source_node_id);
  if (!sourceNode) return null;

  const guardVars = guardWriteVarsForNode(sourceNode.id, wirings);
  if (guardVars.some(g => g.flatVar === binding.source_var)) {
    return { sourceNode, liveVar: binding.source_var, boundVar: binding.source_var, drifted: false };
  }

  const sourceCfg = sourceNode.data.config ?? {};
  const liveVar = sourceCfg.output_var || 'output';
  return { sourceNode, liveVar, boundVar: binding.source_var, drifted: liveVar !== binding.source_var };
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

// setNodes-shaped helper: takes an updater fn (ns => ns2), runs it against `nodes`, returns ns2.
function runSetNodes(nodes, commitFn) {
  let result = null;
  commitFn(updater => { result = updater(nodes); return result; });
  return result;
}

// ── resolveDropTarget / isBindableSource ──────────────────────────────────────

console.log('\nresolveDropTarget / isBindableSource:');

test('llm is bindable with no wirings at all (its own output_var)', () => {
  assert.equal(isBindableSource(node('a', 'llm'), []), true);
  for (const kind of ['condition', 'router', 'hil', 'fork', 'join', 'agent']) {
    assert.equal(isBindableSource(node('a', kind), []), false, `${kind} should not be bindable with no wirings`);
  }
});

test('a non-llm node becomes bindable once it has an enabled guard wiring (docs/APPFLOW_GUARD_OUTPUT_PORTS_PLAN.md Phase 2)', () => {
  const wirings = [{ node_id: 'a', enabled: true, def_slug: 'prompt_inject', config_override: {} }];
  assert.equal(isBindableSource(node('a', 'agent'), wirings), true);
});

test('resolveDragSource: llm with no guards exposes exactly its own output_var', () => {
  const sources = resolveDragSource(node('a', 'llm', { output_var: 'summary' }), []);
  assert.deepEqual(sources, [{ varName: 'summary', label: 'summary' }]);
});

test('resolveDragSource: agent with 2 enabled guards exposes 1 var per guard status, none for its own output (agent never writes one)', () => {
  const wirings = [
    { node_id: 'a', enabled: true, def_slug: 'pii_redact', config_override: { categories: ['email'] } },
    { node_id: 'a', enabled: true, def_slug: 'prompt_inject', config_override: {} },
  ];
  const sources = resolveDragSource(node('a', 'agent'), wirings);
  assert.deepEqual(sources.map(s => s.varName), ['a_pii_redact_status', 'a_pii_redact_email_status', 'a_prompt_inject_status']);
});

test('resolveDragSource: llm WITH a guard exposes both its own output_var and the guard var', () => {
  const wirings = [{ node_id: 'a', enabled: true, def_slug: 'pii_redact', config_override: { categories: ['ssn'] } }];
  const sources = resolveDragSource(node('a', 'llm', { output_var: 'output' }), wirings);
  assert.deepEqual(sources.map(s => s.varName), ['output', 'a_pii_redact_status', 'a_pii_redact_ssn_status']);
});

test('resolveDragSource: a disabled guard contributes nothing', () => {
  const wirings = [{ node_id: 'a', enabled: false, def_slug: 'pii_redact', config_override: {} }];
  assert.deepEqual(resolveDragSource(node('a', 'agent'), wirings), []);
});

test('condition target auto-binds to its single expression field', () => {
  const r = resolveDropTarget(node('t', 'condition'));
  assert.equal(r.kind, 'auto');
  assert.equal(r.field.key, 'expression');
});

test('llm target is ambiguous (system_prompt + user_prompt)', () => {
  const r = resolveDropTarget(node('t', 'llm'));
  assert.equal(r.kind, 'ambiguous');
  assert.deepEqual(r.fields.map(f => f.key), ['system_prompt', 'user_prompt']);
});

test('non-llm/condition target resolves to none', () => {
  for (const kind of ['router', 'hil', 'fork', 'join', 'agent']) {
    assert.equal(resolveDropTarget(node('t', kind)).kind, 'none', `${kind} should resolve to none`);
  }
});

test('undefined target resolves to none', () => {
  assert.equal(resolveDropTarget(undefined).kind, 'none');
});

// ── commitInlinePortBinding ────────────────────────────────────────────────────

console.log('\ncommitInlinePortBinding:');

test('binds a reference to the source NODE (id + var), not a copy of the var name', () => {
  const source = node('src', 'llm', { output_var: 'summary' }, 'Summarizer');
  const target = node('tgt', 'condition', { expression: '' });
  const field = { key: 'expression', label: 'Expression' };

  const result = runSetNodes([source, target], commit => commitInlinePortBinding(source, 'tgt', field, commit));
  const updated = result.find(n => n.id === 'tgt');
  const aliases = updated.data.config.input_aliases;
  const [alias, binding] = Object.entries(aliases)[0];
  assert.equal(binding.source_node_id, 'src');
  assert.equal(binding.source_var, 'summary');
  assert.equal(updated.data.config.expression, `{{.${alias}}}`);
});

test('default alias is "<SourceDisplayName>_output_var" (never dotted — see module doc)', () => {
  const source = node('src', 'llm', { output_var: 'summary' }, 'Summarizer');
  const target = node('tgt', 'condition', {});
  const field = { key: 'expression', label: 'Expression' };

  const result = runSetNodes([source, target], commit => commitInlinePortBinding(source, 'tgt', field, commit));
  const updated = result.find(n => n.id === 'tgt');
  assert.deepEqual(Object.keys(updated.data.config.input_aliases), ['Summarizer_summary']);
});

test('appends to existing non-empty field text rather than replacing it', () => {
  const source = node('src', 'llm', { output_var: 'summary' }, 'Summarizer');
  const target = node('tgt', 'llm', { user_prompt: 'Given the context,' });
  const field = { key: 'user_prompt', label: 'User Prompt' };

  const result = runSetNodes([source, target], commit => commitInlinePortBinding(source, 'tgt', field, commit));
  const updated = result.find(n => n.id === 'tgt');
  assert.equal(updated.data.config.user_prompt, 'Given the context, {{.Summarizer_summary}}');
});

test('accepts an explicit sourceVar override (guard var), instead of always defaulting to output_var', () => {
  const source = node('src', 'agent', {}, 'Agent1');
  const target = node('tgt', 'condition', { expression: '' });
  const field = { key: 'expression', label: 'Expression' };

  const result = runSetNodes([source, target], commit => commitInlinePortBinding(source, 'tgt', field, commit, 'src_pii_redact_email_status'));
  const updated = result.find(n => n.id === 'tgt');
  const [, binding] = Object.entries(updated.data.config.input_aliases)[0];
  assert.equal(binding.source_var, 'src_pii_redact_email_status');
});

test('defaults source var to "output" when output_var is unset', () => {
  const source = node('src', 'llm', {}, 'LLM1');
  const target = node('tgt', 'condition', {});
  const field = { key: 'expression', label: 'Expression' };

  const result = runSetNodes([source, target], commit => commitInlinePortBinding(source, 'tgt', field, commit));
  const aliases = result.find(n => n.id === 'tgt').data.config.input_aliases;
  assert.deepEqual(aliases['LLM1_output'], { source_node_id: 'src', source_var: 'output' });
});

test('two different source nodes both named their output "out" stay distinguishable', () => {
  const src1 = node('src1', 'llm', { output_var: 'out' }, 'LLM1');
  const src2 = node('src2', 'llm', { output_var: 'out' }, 'LLM2');
  const target = node('tgt', 'condition', {});

  let nodes = [src1, src2, target];
  nodes = runSetNodes(nodes, commit => commitInlinePortBinding(src1, 'tgt', { key: 'expression', label: 'Expression' }, commit));
  nodes = runSetNodes(nodes, commit => commitInlinePortBinding(src2, 'tgt', { key: 'expression', label: 'Expression' }, commit));

  const aliases = nodes.find(n => n.id === 'tgt').data.config.input_aliases;
  assert.equal(aliases['LLM1_out'].source_node_id, 'src1');
  assert.equal(aliases['LLM2_out'].source_node_id, 'src2');
});

test('generated alias never contains a literal dot, even if the source display name has one', () => {
  // Regression test: {{.aliasName}} is real Go text/template syntax against a
  // flat map[string]string (go/internal/appflow/inline.go). A dot inside the
  // alias would parse as chained field access ({{.A.B}}), not a literal map
  // key — silently breaking at runtime instead of failing loudly here.
  const source = node('src', 'llm', { output_var: 'output' }, 'Acme, Inc. Summarizer');
  const target = node('tgt', 'condition', {});
  const field = { key: 'expression', label: 'Expression' };

  const result = runSetNodes([source, target], commit => commitInlinePortBinding(source, 'tgt', field, commit));
  const [alias] = Object.keys(result.find(n => n.id === 'tgt').data.config.input_aliases);
  assert.ok(!alias.includes('.'), `alias "${alias}" must not contain a literal dot`);
});

test('collision: two bindings from the SAME source/var pair still get distinct aliases via numeric suffix', () => {
  const source = node('src', 'llm', { output_var: 'output' }, 'LLM1');
  let target = node('tgt', 'llm', { user_prompt: '' });

  let nodes = [source, target];
  nodes = runSetNodes(nodes, commit => commitInlinePortBinding(source, 'tgt', { key: 'user_prompt', label: 'User Prompt' }, commit));
  nodes = runSetNodes(nodes, commit => commitInlinePortBinding(source, 'tgt', { key: 'system_prompt', label: 'System Prompt' }, commit));

  const updated = nodes.find(n => n.id === 'tgt');
  assert.deepEqual(Object.keys(updated.data.config.input_aliases).sort(), ['LLM1_output', 'LLM1_output_2']);
  assert.equal(updated.data.config.user_prompt, '{{.LLM1_output}}');
  assert.equal(updated.data.config.system_prompt, '{{.LLM1_output_2}}');
});

// ── resolveBinding (live rename propagation) ──────────────────────────────────

console.log('\nresolveBinding:');

test('resolves to the source\'s CURRENT output_var, not a frozen copy', () => {
  const source = node('src', 'llm', { output_var: 'summary' }, 'Summarizer');
  const target = node('tgt', 'condition', { input_aliases: { Summarizer_summary: { source_node_id: 'src', source_var: 'summary' } } });

  const binding = resolveBinding(target, 'Summarizer_summary', [source, target]);
  assert.equal(binding.liveVar, 'summary');
  assert.equal(binding.drifted, false);
});

test('detects drift after the source renames its output_var, but still resolves correctly', () => {
  const renamedSource = node('src', 'llm', { output_var: 'sentiment' }, 'Summarizer'); // renamed after binding
  const target = node('tgt', 'condition', { input_aliases: { Summarizer_summary: { source_node_id: 'src', source_var: 'summary' } } });

  const binding = resolveBinding(target, 'Summarizer_summary', [renamedSource, target]);
  assert.equal(binding.liveVar, 'sentiment');
  assert.equal(binding.boundVar, 'summary');
  assert.equal(binding.drifted, true);
  assert.equal(binding.sourceNode.id, 'src');
});

test('returns null when the source node no longer exists', () => {
  const target = node('tgt', 'condition', { input_aliases: { Ghost_output: { source_node_id: 'deleted', source_var: 'output' } } });
  assert.equal(resolveBinding(target, 'Ghost_output', [target]), null);
});

test('returns null for a var that is not a drag-created alias', () => {
  const target = node('tgt', 'condition', { input_aliases: {} });
  assert.equal(resolveBinding(target, 'typed_var', [target]), null);
});

test('a bound guard var never reports drift, even though it does not match the source\'s output_var', () => {
  // Regression case this Phase 2 change specifically had to avoid: naively
  // reusing the old liveVar=output_var comparison would flag EVERY guard-var
  // binding as "drifted" (a guard var name never equals output_var).
  const source = node('src', 'agent', {}, 'Agent1');
  const target = node('tgt', 'condition', { input_aliases: { pii_email: { source_node_id: 'src', source_var: 'src_pii_redact_email_status' } } });
  const wirings = [{ node_id: 'src', enabled: true, def_slug: 'pii_redact', config_override: { categories: ['email'] } }];

  const binding = resolveBinding(target, 'pii_email', [source, target], wirings);
  assert.equal(binding.drifted, false);
  assert.equal(binding.liveVar, 'src_pii_redact_email_status');
});

// ── renameInlinePortAlias ──────────────────────────────────────────────────────

console.log('\nrenameInlinePortAlias:');

test('renames the alias key and rewrites every {{.alias}} occurrence in the node\'s text fields', () => {
  const target = node('tgt', 'llm', {
    input_aliases: { LLM1_output: { source_node_id: 'src', source_var: 'output' } },
    system_prompt: 'You know {{.LLM1_output}}.',
    user_prompt: 'Also: {{.LLM1_output}}',
  });

  const result = runSetNodes([target], commit => renameInlinePortAlias('tgt', 'LLM1_output', 'sentiment', commit));
  const updated = result.find(n => n.id === 'tgt');
  assert.deepEqual(Object.keys(updated.data.config.input_aliases), ['sentiment']);
  assert.equal(updated.data.config.input_aliases.sentiment.source_node_id, 'src');
  assert.equal(updated.data.config.system_prompt, 'You know {{.sentiment}}.');
  assert.equal(updated.data.config.user_prompt, 'Also: {{.sentiment}}');
});

test('no-op when old and new alias are identical (setNodes never called)', () => {
  let called = false;
  renameInlinePortAlias('tgt', 'output', 'output', () => { called = true; });
  assert.equal(called, false);
});

test('no-op when the alias does not exist on the node', () => {
  const target = node('tgt', 'llm', { input_aliases: {}, user_prompt: 'plain text' });
  const result = runSetNodes([target], commit => renameInlinePortAlias('tgt', 'ghost', 'new_name', commit));
  assert.deepEqual(result.find(n => n.id === 'tgt').data.config.input_aliases, {});
});

// ── deleteInlinePortAlias ──────────────────────────────────────────────────────

console.log('\ndeleteInlinePortAlias:');

test('removes the alias entry and strips {{.alias}} (with leading space) from text fields', () => {
  const target = node('tgt', 'llm', {
    input_aliases: { LLM1_output: { source_node_id: 'src', source_var: 'output' } },
    user_prompt: 'Given the context, {{.LLM1_output}}',
  });
  const result = runSetNodes([target], commit => deleteInlinePortAlias('tgt', 'LLM1_output', commit));
  const updated = result.find(n => n.id === 'tgt');
  assert.deepEqual(updated.data.config.input_aliases, {});
  assert.equal(updated.data.config.user_prompt, 'Given the context,');
});

test('leaves other aliases and their template refs untouched', () => {
  const target = node('tgt', 'llm', {
    input_aliases: {
      a: { source_node_id: 'src_a', source_var: 'a' },
      b: { source_node_id: 'src_b', source_var: 'b' },
    },
    user_prompt: '{{.a}} and {{.b}}',
  });
  const result = runSetNodes([target], commit => deleteInlinePortAlias('tgt', 'a', commit));
  const updated = result.find(n => n.id === 'tgt');
  assert.deepEqual(Object.keys(updated.data.config.input_aliases), ['b']);
  // Only the leading space directly attached to the deleted {{.a}} ref is
  // stripped — a leftover leading space before "and" is a cosmetic artifact
  // of appendTemplateRef's own space-prefixing convention, not a bug.
  assert.equal(updated.data.config.user_prompt, ' and {{.b}}');
});

// ── Summary ───────────────────────────────────────────────────────────────────

console.log(`\n${passed + failed} tests: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
