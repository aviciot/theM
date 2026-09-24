/**
 * Extracts `{{.varname}}` / `{{varname}}` Go-template variable references from
 * free-text prompt/expression strings.
 *
 * Shared by the agent builder (agentgen, via nodeVars.ts's re-export) and
 * AppFlow (docs/APPFLOW_NAMED_PORTS_PLAN.md) — both runtimes read variables
 * out of a flat, shared string-keyed var bag using the same Go text/template
 * `{{.x}}` syntax, so the extraction regex is identical for both.
 */
export function extractTemplateVars(tmpl: string): string[] {
  const matches: string[] = [];
  const re = /\{\{\.?(\w+)\}\}/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(tmpl)) !== null) matches.push(m[1]);
  return [...new Set(matches)];
}
