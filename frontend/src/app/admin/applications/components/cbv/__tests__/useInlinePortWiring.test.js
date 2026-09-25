/**
 * Tests for useInlinePortWiring.ts — Phase 5 drag-to-connect named data-port
 * wiring (docs/APPFLOW_NAMED_PORTS_PLAN.md).
 *
 * Run with: node src/app/admin/applications/components/cbv/__tests__/useInlinePortWiring.test.js
 *
 * Inlines the pure functions (no TypeScript runtime needed), following the
 * convention established by appFlowVars.test.js.
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

function isBindableSource(node) {
  return !!node && node.data.node_type === 'llm';
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

function uniqueAlias(desired, existing) {
  if (!(desired in existing)) return desired;
  let i = 2;
  while (`${desired}_${i}` in existing) i++;
  return `${desired}_${i}`;
}

function appendTemplateRef(text, alias) {
  const ref = `{{.${alias}}}`;
  const trimmed = text.trim();
  return trimmed ? `${text} ${ref}` : ref;
}

function commitInlinePortBinding(sourceNode, targetNodeId, field, setNodes) {
  const sourceCfg = sourceNode.data.config ?? {};
  const sourceVar = sourceCfg.output_var || 'output';

  setNodes(ns => ns.map(n => {
    if (n.id !== targetNodeId) return n;
    const nd = n.data;
    const cfg = nd.config ?? {};
    const existingAliases = cfg.input_aliases ?? {};
    const alias = uniqueAlias(sourceVar, existingAliases);
    const currentText = cfg[field.key] || '';
    return {
      ...n,
      data: {
        ...nd,
        config: {
          ...cfg,
          input_aliases: { ...existingAliases, [alias]: sourceVar },
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

function node(id, node_type, config = {}) {
  return { id, data: { node_type, config } };
}

// setNodes-shaped helper: takes an updater fn (ns => ns2), runs it against `nodes`, returns ns2.
function runSetNodes(nodes, commitFn) {
  let result = null;
  commitFn(updater => { result = updater(nodes); return result; });
  return result;
}

// ── resolveDropTarget / isBindableSource ──────────────────────────────────────

console.log('\nresolveDropTarget / isBindableSource:');

test('llm is the only bindable source', () => {
  assert.equal(isBindableSource(node('a', 'llm')), true);
  for (const kind of ['condition', 'router', 'hil', 'fork', 'join', 'agent']) {
    assert.equal(isBindableSource(node('a', kind)), false, `${kind} should not be bindable`);
  }
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

test('binds source output_var as a new alias, appends {{.alias}} to an empty field', () => {
  const source = node('src', 'llm', { output_var: 'summary' });
  const target = node('tgt', 'condition', { expression: '' });
  const field = { key: 'expression', label: 'Expression' };

  const result = runSetNodes([source, target], commit => commitInlinePortBinding(source, 'tgt', field, commit));
  const updated = result.find(n => n.id === 'tgt');
  assert.deepEqual(updated.data.config.input_aliases, { summary: 'summary' });
  assert.equal(updated.data.config.expression, '{{.summary}}');
});

test('appends to existing non-empty field text rather than replacing it', () => {
  const source = node('src', 'llm', { output_var: 'summary' });
  const target = node('tgt', 'llm', { user_prompt: 'Given the context,' });
  const field = { key: 'user_prompt', label: 'User Prompt' };

  const result = runSetNodes([source, target], commit => commitInlinePortBinding(source, 'tgt', field, commit));
  const updated = result.find(n => n.id === 'tgt');
  assert.equal(updated.data.config.user_prompt, 'Given the context, {{.summary}}');
});

test('defaults source var to "output" when output_var is unset', () => {
  const source = node('src', 'llm', {});
  const target = node('tgt', 'condition', {});
  const field = { key: 'expression', label: 'Expression' };

  const result = runSetNodes([source, target], commit => commitInlinePortBinding(source, 'tgt', field, commit));
  assert.deepEqual(result.find(n => n.id === 'tgt').data.config.input_aliases, { output: 'output' });
});

test('collision: second binding of the same var name gets a numeric suffix, matching agent builder', () => {
  const source = node('src', 'llm', { output_var: 'output' });
  let target = node('tgt', 'llm', { user_prompt: '' });

  let nodes = [source, target];
  nodes = runSetNodes(nodes, commit => commitInlinePortBinding(source, 'tgt', { key: 'user_prompt', label: 'User Prompt' }, commit));
  nodes = runSetNodes(nodes, commit => commitInlinePortBinding(source, 'tgt', { key: 'system_prompt', label: 'System Prompt' }, commit));

  const updated = nodes.find(n => n.id === 'tgt');
  assert.deepEqual(updated.data.config.input_aliases, { output: 'output', output_2: 'output' });
  assert.equal(updated.data.config.user_prompt, '{{.output}}');
  assert.equal(updated.data.config.system_prompt, '{{.output_2}}');
});

// ── renameInlinePortAlias ──────────────────────────────────────────────────────

console.log('\nrenameInlinePortAlias:');

test('renames the alias key and rewrites every {{.alias}} occurrence in the node\'s text fields', () => {
  const target = node('tgt', 'llm', {
    input_aliases: { output: 'output' },
    system_prompt: 'You know {{.output}}.',
    user_prompt: 'Also: {{.output}}',
  });

  const result = runSetNodes([target], commit => renameInlinePortAlias('tgt', 'output', 'sentiment', commit));
  const updated = result.find(n => n.id === 'tgt');
  assert.deepEqual(updated.data.config.input_aliases, { sentiment: 'output' });
  assert.equal(updated.data.config.system_prompt, 'You know {{.sentiment}}.');
  assert.equal(updated.data.config.user_prompt, 'Also: {{.sentiment}}');
});

test('no-op when old and new alias are identical (setNodes never called)', () => {
  const target = node('tgt', 'llm', { input_aliases: { output: 'output' }, user_prompt: '{{.output}}' });
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
    input_aliases: { output: 'output' },
    user_prompt: 'Given the context, {{.output}}',
  });
  const result = runSetNodes([target], commit => deleteInlinePortAlias('tgt', 'output', commit));
  const updated = result.find(n => n.id === 'tgt');
  assert.deepEqual(updated.data.config.input_aliases, {});
  assert.equal(updated.data.config.user_prompt, 'Given the context,');
});

test('leaves other aliases and their template refs untouched', () => {
  const target = node('tgt', 'llm', {
    input_aliases: { a: 'a', b: 'b' },
    user_prompt: '{{.a}} and {{.b}}',
  });
  const result = runSetNodes([target], commit => deleteInlinePortAlias('tgt', 'a', commit));
  const updated = result.find(n => n.id === 'tgt');
  assert.deepEqual(updated.data.config.input_aliases, { b: 'b' });
  // Only the leading space directly attached to the deleted {{.a}} ref is
  // stripped — a leftover leading space before "and" is a cosmetic artifact
  // of appendTemplateRef's own space-prefixing convention, not a bug.
  assert.equal(updated.data.config.user_prompt, ' and {{.b}}');
});

// ── Summary ───────────────────────────────────────────────────────────────────

console.log(`\n${passed + failed} tests: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
