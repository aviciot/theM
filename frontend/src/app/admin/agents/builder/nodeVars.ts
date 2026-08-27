import type { Node } from '@xyflow/react';
import type { StepData } from './types';

export interface NodeVars {
  reads: string[];
  writes: string[];
}

function extractTemplateVars(tmpl: string): string[] {
  const matches: string[] = [];
  const re = /\{\{\.?(\w+)\}\}/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(tmpl)) !== null) matches.push(m[1]);
  return [...new Set(matches)];
}

export function extractNodeVars(node: Node): NodeVars {
  const d = node.data as unknown as StepData;
  const cfg = d.config ?? {};
  const st = d.step_type;

  if (st === 'input') {
    const bindVar = (cfg.bindings as Record<string, string>)?.text || 'input';
    return { reads: [], writes: [bindVar] };
  }

  if (st === 'llm') {
    const userPrompt = (cfg.user_prompt as string) || '';
    const systemPrompt = (cfg.system_prompt as string) || '';
    const outVar = (cfg.output_var as string) || 'output';
    const reads = [...new Set([...extractTemplateVars(userPrompt), ...extractTemplateVars(systemPrompt)])];
    return { reads, writes: [outVar] };
  }

  if (st === 'transform') {
    const functions = (cfg.functions as Array<{ fn: string; input_var: string; output_var: string; args?: Record<string, unknown> }>) ?? [];
    const reads: string[] = [];
    const writes: string[] = [];
    for (const f of functions) {
      if (f.input_var) reads.push(f.input_var);
      if (f.output_var) writes.push(f.output_var);
      if (f.fn === 'template' && f.args?.template) {
        reads.push(...extractTemplateVars(String(f.args.template)));
      }
    }
    const exprs = (cfg.expressions as Record<string, string>) ?? {};
    for (const [outKey, tmpl] of Object.entries(exprs)) {
      reads.push(...extractTemplateVars(tmpl));
      writes.push(outKey);
    }
    const extractions = (cfg.extractions as Array<{ from_var: string; var: string }>) ?? [];
    for (const ext of extractions) {
      if (ext.from_var) reads.push(ext.from_var);
      if (ext.var) writes.push(ext.var);
    }
    return { reads: [...new Set(reads)], writes: [...new Set(writes)] };
  }

  if (st === 'http') {
    const urlTemplate = (cfg.url_template as string) || '';
    const bodyTemplate = (cfg.body_template as string) || '';
    const reads = [...new Set([...extractTemplateVars(urlTemplate), ...extractTemplateVars(bodyTemplate)])];
    return { reads, writes: ['http_response'] };
  }

  if (st === 'response') {
    const fromVar = (cfg.from_var as string) || 'output';
    return { reads: [fromVar], writes: [] };
  }

  if (st === 'branch') {
    const expr = (cfg.expression as string) || '';
    return { reads: extractTemplateVars(expr), writes: [] };
  }

  return { reads: [], writes: [] };
}

export function edgeRelevantVars(sourceNode: Node, targetNode: Node): string[] {
  const { writes } = extractNodeVars(sourceNode);
  const { reads } = extractNodeVars(targetNode);
  if (reads.length === 0) return writes;
  const intersection = writes.filter(v => reads.includes(v));
  return intersection.length > 0 ? intersection : writes;
}
