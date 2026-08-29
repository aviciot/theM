import { useState } from 'react';
import { themApi, getPreferences, setPreferences } from '@/lib/api';
import { getNodeDef } from '@/lib/nodeRegistry';
import { INITIAL_DEBUG } from '../constants';
import { PROVIDER_LIST, RUNTIME_MODELS } from '../../../applications/constants';
import { edgeRelevantVars } from '../nodeVars';
import type { DebugNodeState, DebugParamSpec, DebugState, StepData } from '../types';
import type { Edge, Node } from '@xyflow/react';
import { topoSort } from '../canvas/connections';

interface UseDebugSessionParams {
  defId: string | null;
  activeSkillId: string | null;
  localPipeNodes: Node[];
  localPipeEdges: Edge[];
}

export function useDebugSession({ defId, activeSkillId, localPipeNodes, localPipeEdges }: UseDebugSessionParams) {
  const [debug, setDebug] = useState<DebugState>(INITIAL_DEBUG);

  function ssKey(paramKey: string) { return `debug_param:${defId ?? 'new'}:${paramKey}`; }
  function ssGet(paramKey: string) { try { return sessionStorage.getItem(ssKey(paramKey)) ?? ''; } catch { return ''; } }
  function ssSet(paramKey: string, val: string) { try { sessionStorage.setItem(ssKey(paramKey), val); } catch { /* ignore */ } }

  function buildDebugParamSpecs(nodes: Node[]) {
    const specs: DebugParamSpec[] = [];
    const seenParamKeys = new Set<string>();

    specs.push({
      key: '__test_input',
      label: 'Test message',
      description: 'The user message to send into the pipeline',
      isSecret: false,
      required: true,
    });

    const hasLLM = nodes.some(n => (n.data as unknown as StepData).step_type === 'llm');
    if (hasLLM) {
      specs.push({
        key: '__debug_provider',
        label: 'LLM Provider',
        description: 'Provider to use for all LLM nodes in this debug run',
        isSecret: false,
        required: true,
        options: [...PROVIDER_LIST],
      });
      specs.push({
        key: '__debug_model',
        label: 'Model',
        description: 'Model to use (must be valid for the chosen provider)',
        isSecret: false,
        required: true,
        options: Object.values(RUNTIME_MODELS).flat(),
      });
      specs.push({
        key: '__debug_api_key',
        label: 'API Key',
        description: 'API key for the chosen provider — stored in browser session only, never sent to the-M server',
        isSecret: true,
        required: true,
      });
    }

    for (const node of nodes) {
      const d = node.data as unknown as StepData;
      if (d.step_type !== 'http') continue;
      const paramKey = d.config?.app_param_key as string | undefined;
      if (!paramKey || seenParamKeys.has(paramKey)) continue;
      seenParamKeys.add(paramKey);

      const httpDef = getNodeDef('http');
      const decl = httpDef.app_params?.find(p => p.key === paramKey);
      specs.push({
        key: paramKey,
        label: decl?.label ?? paramKey,
        description: decl?.description ?? `Required by HTTP node "${d.label || d.step_id}"`,
        isSecret: decl?.type === 'secret',
        required: true,
        nodeLabel: d.label || d.step_id,
      });
    }

    return specs;
  }

  async function loadDebugPrefs(): Promise<{ testInput: string }> {
    try {
      const prefs = await getPreferences();
      const saved = (prefs as Record<string, unknown>).debugValues as Record<string, { testInput: string }> | undefined;
      return { testInput: saved?.[defId ?? 'new']?.testInput ?? '' };
    } catch { return { testInput: '' }; }
  }

  async function saveDebugPrefs(testInput: string) {
    try {
      const prefs = await getPreferences();
      const existing = (prefs as Record<string, unknown>).debugValues as Record<string, unknown> ?? {};
      await setPreferences({ ...prefs, debugValues: { ...existing, [defId ?? 'new']: { testInput } } });
    } catch { /* non-critical */ }
  }

  function renderTemplate(template: string, vars: Record<string, unknown>): string {
    return template.replace(/\{\{\.?(\w+)\}\}/g, (_, key) => String(vars[key] ?? ''));
  }

  async function executeStep(
    nodeId: string,
    nodes: Node[],
    edges: Edge[],
    vars: Record<string, unknown>,
    debugParams: Record<string, string>,
  ): Promise<{ vars: Record<string, unknown>; output: string; edgeValues: Record<string, string> }> {
    const node = nodes.find(n => n.id === nodeId);
    if (!node) throw new Error(`Node ${nodeId} not found`);
    const d = node.data as unknown as StepData;
    const cfg = d.config ?? {};
    const newVars = { ...vars };
    let output = '';
    const edgeValues: Record<string, string> = {};

    const outEdgesForNode = edges.filter(e => e.source === nodeId);

    if (d.step_type === 'input') {
      const bindVar = (cfg.bindings as Record<string,string>)?.text || 'input';
      newVars[bindVar] = newVars[bindVar] ?? '';
      output = String(newVars[bindVar]);
      for (const e of outEdgesForNode) edgeValues[e.id] = output;
    } else if (d.step_type === 'llm') {
      const model = (cfg.model as string) || 'claude-haiku-4-5-20251001';
      const maxTokens = (cfg.max_tokens as number) || 4096;
      const systemPrompt = (cfg.system_prompt as string) || '';
      const userPromptTemplate = (cfg.user_prompt as string) || '';

      const inEdge = edges.find(e => e.target === nodeId);
      const inSourceNode = inEdge ? nodes.find(n => n.id === inEdge.source) : undefined;
      const inBindVar = inSourceNode
        ? ((inSourceNode.data as unknown as StepData).config?.bindings as Record<string,string>)?.text || 'input'
        : 'input';

      const userPrompt = userPromptTemplate
        ? renderTemplate(userPromptTemplate, newVars)
        : String(newVars[inBindVar] ?? '');
      const outVar = (cfg.output_var as string) || 'output';

      const messages: { role: string; content: string }[] = [];
      if (userPrompt) messages.push({ role: 'user', content: userPrompt });
      if (messages.length === 0) throw new Error('LLM step: user prompt is empty — connect an Input node or set the user_prompt template.');

      const debugProvider = debugParams['__debug_provider'] ?? 'anthropic';
      const debugModel = debugParams['__debug_model'] || model;
      const resp = await fetch('/api/debug/llm', {
        method: 'POST',
        headers: {
          'content-type': 'application/json',
          'x-debug-provider': debugProvider,
          'x-debug-api-key': debugParams['__debug_api_key'] ?? '',
        },
        body: JSON.stringify({
          model: debugModel,
          max_tokens: maxTokens,
          ...(systemPrompt ? { system: systemPrompt } : {}),
          messages,
        }),
      });
      if (!resp.ok) {
        const errText = await resp.text();
        throw new Error(`Anthropic API error ${resp.status}: ${errText.slice(0, 200)}`);
      }
      const json = await resp.json() as { content: { type: string; text: string }[] };
      const text = json.content?.find(c => c.type === 'text')?.text ?? '';
      newVars[outVar] = text;
      output = text;
      for (const e of outEdgesForNode) edgeValues[e.id] = text;
    } else if (d.step_type === 'response') {
      const fromVar = (cfg.from_var as string) || 'output';
      output = String(newVars[fromVar] ?? '');
    } else if (d.step_type === 'transform') {
      const functions = (cfg.functions as Array<{ fn: string; args?: Record<string, unknown>; input_var: string; output_var: string }>) ?? [];
      for (const f of functions) {
        const raw = newVars[f.input_var];
        const rawStr = raw === undefined ? '' : typeof raw === 'string' ? raw : JSON.stringify(raw);
        let result: unknown = rawStr;
        if (f.fn === 'strip_fences') {
          result = rawStr.replace(/^```[a-z]*\n?/i, '').replace(/\n?```\s*$/i, '').trim();
        } else if (f.fn === 'json_path') {
          const path = String((f.args?.path as string) ?? '');
          let parsed: unknown = null;
          if (typeof raw === 'object' && raw !== null) parsed = raw;
          else { try { parsed = JSON.parse(rawStr); } catch { parsed = null; } }
          if (parsed !== null) {
            const parts = path.replace(/^\$\.?/, '').split('.').filter(Boolean);
            let cur: unknown = parsed;
            for (const p of parts) { if (typeof cur === 'object' && cur !== null) cur = (cur as Record<string, unknown>)[p]; else { cur = undefined; break; } }
            result = cur !== undefined ? cur : '';
          }
        } else if (f.fn === 'template') {
          result = renderTemplate(rawStr, newVars);
        }
        newVars[f.output_var] = result;
        output = typeof result === 'string' ? result : JSON.stringify(result);
      }
      const exprs = (cfg.expressions as Record<string, string>) ?? {};
      for (const [outKey, tmpl] of Object.entries(exprs)) {
        const val = renderTemplate(tmpl, newVars);
        newVars[outKey] = val;
        output = val;
      }
      const extractions = (cfg.extractions as Array<{ from_var: string; json_path: string; var: string }>) ?? [];
      for (const ext of extractions) {
        const raw = newVars[ext.from_var];
        if (raw === undefined) continue;
        let parsed: Record<string, unknown> | null = null;
        if (typeof raw === 'object' && raw !== null) parsed = raw as Record<string, unknown>;
        else if (typeof raw === 'string') { try { parsed = JSON.parse(raw); } catch { continue; } }
        if (!parsed) continue;
        const parts = ext.json_path.replace(/^\$\./, '').split('.');
        let cur: unknown = parsed;
        for (const p of parts) { if (typeof cur === 'object' && cur !== null) cur = (cur as Record<string, unknown>)[p]; else { cur = undefined; break; } }
        if (cur !== undefined) { newVars[ext.var] = String(cur); output = String(cur); }
      }
      for (const e of outEdgesForNode) edgeValues[e.id] = output;
    } else if (d.step_type === 'branch') {
      const expr = (cfg.expression as string) || '';
      const trueNext = (cfg.true_next as string) || '';
      const falseNext = (cfg.false_next as string) || '';
      const rendered = renderTemplate(expr, newVars);
      const truthy = rendered.trim() !== '' && rendered.trim() !== 'false' && rendered.trim() !== '0' && rendered.trim() !== '<no value>';
      output = truthy ? `→ ${trueNext} (true)` : `→ ${falseNext} (false)`;
      for (const e of outEdgesForNode) edgeValues[e.id] = truthy ? 'true' : 'false';
    } else if (d.step_type === 'http') {
      const method = (cfg.method as string) || 'GET';
      const urlTemplate = (cfg.url_template as string) || '';
      const bodyTemplate = (cfg.body_template as string) || '';
      const appParamKey = (cfg.app_param_key as string) || '';
      const injectMode = (cfg.inject_mode as string) || 'header';
      const injectHeaderName = (cfg.inject_header_name as string) || 'api_key';

      let url = renderTemplate(urlTemplate, newVars);
      const headers: Record<string, string> = { 'Accept': 'application/json' };

      if (appParamKey && debugParams[appParamKey]) {
        const paramVal = debugParams[appParamKey];
        if (injectMode === 'query') {
          const sep = url.includes('?') ? '&' : '?';
          url += `${sep}${encodeURIComponent(injectHeaderName)}=${encodeURIComponent(paramVal)}`;
        } else if (injectMode === 'basic') {
          headers['Authorization'] = 'Basic ' + btoa(paramVal);
        } else if (injectMode === 'custom_header') {
          headers[injectHeaderName] = paramVal;
        } else {
          headers['Authorization'] = 'Bearer ' + paramVal;
        }
      }

      const proxyBody: Record<string, unknown> = { method, url, headers };
      if (bodyTemplate && method !== 'GET') {
        proxyBody.body = renderTemplate(bodyTemplate, newVars);
        (headers as Record<string, string>)['Content-Type'] = 'application/json';
      }
      const resp = await fetch('/api/v1/admin/debug-proxy', {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
        body: JSON.stringify(proxyBody),
      });
      if (!resp.ok) {
        const errText = await resp.text();
        throw new Error(errText.slice(0, 120) || `HTTP ${resp.status}`);
      }
      const text = await resp.text();
      try {
        const parsed = JSON.parse(text);
        newVars['http_response'] = parsed;
      } catch {
        newVars['http_response'] = text;
      }
      output = text;
      for (const e of outEdgesForNode) edgeValues[e.id] = text;
    } else {
      output = `[${d.step_type} not supported in debug mode]`;
    }

    for (const e of outEdgesForNode) {
      const targetNode = nodes.find(n => n.id === e.target);
      if (!targetNode) continue;
      const relevant = edgeRelevantVars(node, targetNode);
      if (relevant.length === 0) {
        continue;
      }
      const label = relevant
        .map(v => `${v}: ${String(newVars[v] ?? '').slice(0, 60)}`)
        .filter(s => !s.endsWith(': '))
        .join('\n');
      if (label) edgeValues[e.id] = label;
    }

    return { vars: newVars, output, edgeValues };
  }

  function debugReset() {
    setDebug(prev => ({
      ...INITIAL_DEBUG,
      active: prev.active,
      setupComplete: false,
      paramSpecs: prev.paramSpecs,
      debugParams: prev.debugParams,
    }));
  }

  async function debugStartSetup() {
    if (!activeSkillId) {
      setDebug(prev => ({
        ...prev,
        active: true,
        setupComplete: false,
        paramSpecs: [],
        debugParams: {},
        error: 'Open a skill first — double-click a skill node on the canvas, then click Debug.',
      }));
      return;
    }

    const baseSpecs = buildDebugParamSpecs(localPipeNodes).filter(
      s => s.key === '__test_input' || s.key === '__debug_provider' || s.key === '__debug_model' || s.key === '__debug_api_key',
    );

    let apiSpecs: import('@/lib/api').AgentParamMeta[] = [];
    if (defId) {
      try {
        const resp = await themApi.getDefinitionParams(defId);
        apiSpecs = resp.required_params ?? [];
      } catch {
        // Draft not yet published — fall back to raw node scan below.
      }
    }

    let paramSpecs: DebugParamSpec[];
    if (apiSpecs.length > 0) {
      const extraSpecs: DebugParamSpec[] = apiSpecs.map(p => ({
        key: p.key,
        label: p.label,
        description: p.description,
        isSecret: p.type === 'secret',
        required: p.required,
      }));
      paramSpecs = [...baseSpecs, ...extraSpecs];
    } else {
      paramSpecs = buildDebugParamSpecs(localPipeNodes);
    }

    const prefs = await loadDebugPrefs();
    const params: Record<string, string> = {};
    for (const spec of paramSpecs) {
      if (spec.key === '__test_input') params[spec.key] = prefs.testInput;
      else params[spec.key] = ssGet(spec.key);
    }
    setDebug(prev => ({
      ...prev,
      active: true,
      setupComplete: false,
      paramSpecs,
      debugParams: params,
      error: null,
    }));
  }

  async function debugCommitSetup() {
    const testInput = debug.debugParams['__test_input'] ?? '';
    if (!testInput.trim()) { setDebug(prev => ({ ...prev, error: 'Test message is required.' })); return; }
    const hasLLM = debug.paramSpecs.some(s => s.key === '__debug_provider');
    if (hasLLM) {
      if (!(debug.debugParams['__debug_provider'] ?? '').trim()) {
        setDebug(prev => ({ ...prev, error: 'Select an LLM provider for debug.' })); return;
      }
      if (!(debug.debugParams['__debug_model'] ?? '').trim()) {
        setDebug(prev => ({ ...prev, error: 'Select a model for debug.' })); return;
      }
      if (!(debug.debugParams['__debug_api_key'] ?? '').trim()) {
        setDebug(prev => ({ ...prev, error: 'Enter an API key for the chosen provider.' })); return;
      }
    }
    saveDebugPrefs(testInput);
    for (const spec of debug.paramSpecs) {
      if (spec.isSecret) ssSet(spec.key, debug.debugParams[spec.key] ?? '');
    }
    setDebug(prev => ({ ...prev, setupComplete: true, error: null }));
  }

  async function debugRunAll() {
    if (!debug.setupComplete) return;
    const testInput = debug.debugParams['__test_input'] ?? '';
    const order = topoSort(localPipeNodes, localPipeEdges);
    if (!order) { setDebug(prev => ({ ...prev, error: 'Pipeline has a cycle — cannot execute.' })); return; }

    setDebug(prev => ({
      ...prev, mode: 'run-all', error: null, executionOrder: order,
      nodeStates: Object.fromEntries(order.map(id => [id, 'pending' as DebugNodeState])),
      nodeOutputs: {}, nodeErrors: {}, edgeValues: {}, vars: {}, nodeInputVars: {},
    }));

    const inputNode = localPipeNodes.find(n => (n.data as unknown as StepData).step_type === 'input');
    let vars: Record<string, unknown> = {};
    if (inputNode) {
      const inputData = inputNode.data as unknown as StepData;
      const bindVar = (inputData.config?.bindings as Record<string,string>)?.text || 'input';
      vars[bindVar] = testInput;
    }

    for (const nodeId of order) {
      const inputSnapshot = { ...vars };
      setDebug(prev => ({ ...prev, vars, nodeStates: { ...prev.nodeStates, [nodeId]: 'running' }, nodeInputVars: { ...prev.nodeInputVars, [nodeId]: inputSnapshot } }));
      try {
        const result = await executeStep(nodeId, localPipeNodes, localPipeEdges, vars, debug.debugParams);
        vars = result.vars;
        setDebug(prev => ({
          ...prev, vars,
          edgeValues: { ...prev.edgeValues, ...result.edgeValues },
          nodeStates: { ...prev.nodeStates, [nodeId]: 'done' },
          nodeOutputs: { ...prev.nodeOutputs, [nodeId]: result.output },
        }));
      } catch (err) {
        const msg = String(err);
        setDebug(prev => ({
          ...prev,
          nodeStates: { ...prev.nodeStates, [nodeId]: 'error' },
          nodeErrors: { ...prev.nodeErrors, [nodeId]: msg },
          error: `Step failed: ${msg}`,
        }));
        return;
      }
    }
    setDebug(prev => ({ ...prev, currentStepIndex: order.length }));
  }

  async function debugStep() {
    if (!debug.setupComplete) return;
    const testInput = debug.debugParams['__test_input'] ?? '';

    if (!debug.mode) {
      const order = topoSort(localPipeNodes, localPipeEdges);
      if (!order) { setDebug(prev => ({ ...prev, error: 'Pipeline has a cycle.' })); return; }

      const inputNode = localPipeNodes.find(n => (n.data as unknown as StepData).step_type === 'input');
      let initVars: Record<string, unknown> = {};
      if (inputNode) {
        const inputData = inputNode.data as unknown as StepData;
        const bindVar = (inputData.config?.bindings as Record<string,string>)?.text || 'input';
        initVars[bindVar] = testInput;
      }

      const firstNodeId = order[0];
      setDebug(prev => ({
        ...prev, mode: 'step', error: null, executionOrder: order, currentStepIndex: 0,
        vars: initVars,
        nodeStates: { ...Object.fromEntries(order.map(id => [id, 'idle' as DebugNodeState])), [firstNodeId]: 'pending' },
        nodeOutputs: {}, nodeErrors: {}, edgeValues: {}, pendingVarOverrides: {}, nodeInputVars: {},
      }));
      return;
    }

    const { executionOrder, currentStepIndex, vars } = debug;
    if (currentStepIndex >= executionOrder.length) return;

    const nodeId = executionOrder[currentStepIndex];
    const mergedVars = { ...vars, ...debug.pendingVarOverrides };

    setDebug(prev => ({
      ...prev,
      nodeStates: { ...prev.nodeStates, [nodeId]: 'running' },
      nodeInputVars: { ...prev.nodeInputVars, [nodeId]: { ...mergedVars } },
      pendingVarOverrides: {},
    }));

    try {
      const result = await executeStep(nodeId, localPipeNodes, localPipeEdges, mergedVars, debug.debugParams);
      const nextIdx = currentStepIndex + 1;
      const nextNodeId = executionOrder[nextIdx];
      setDebug(prev => ({
        ...prev,
        vars: result.vars,
        edgeValues: { ...prev.edgeValues, ...result.edgeValues },
        nodeStates: {
          ...prev.nodeStates,
          [nodeId]: 'done',
          ...(nextNodeId ? { [nextNodeId]: 'pending' } : {}),
        },
        nodeOutputs: { ...prev.nodeOutputs, [nodeId]: result.output },
        currentStepIndex: nextIdx,
      }));
    } catch (err) {
      const msg = String(err);
      setDebug(prev => ({
        ...prev,
        nodeStates: { ...prev.nodeStates, [nodeId]: 'error' },
        nodeErrors: { ...prev.nodeErrors, [nodeId]: msg },
        error: `Step failed: ${msg}`,
      }));
    }
  }

  return {
    debug, setDebug,
    debugReset,
    debugStartSetup,
    debugCommitSetup,
    debugRunAll,
    debugStep,
  };
}
