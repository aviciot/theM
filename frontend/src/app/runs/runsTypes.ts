import type { RunDetail, RunStep, TaskOut, ArtifactOut } from '@/lib/api';

// ── Helpers ───────────────────────────────────────────────────────────────────

export function formatDuration(ms: number | null) {
  if (ms == null) return '—';
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

export function formatTs(iso: string) {
  return new Date(iso).toLocaleString();
}

export const STATUS_COLOR: Record<string, string> = {
  completed: '#4edea3', failed: '#f87171', running: '#5b7fff',
  pending: '#fbbf24', working: '#a78bfa', submitted: '#60a5fa',
  canceled: '#94a3b8', rejected: '#94a3b8',
};

export function statusColor(s: string) { return STATUS_COLOR[s] ?? '#94a3b8'; }

// ── Node graph types ──────────────────────────────────────────────────────────

export type GraphNode =
  | { kind: 'user';         text: string }
  | { kind: 'orchestrator'; run: RunDetail }
  | { kind: 'iteration';    iteration: number }
  | { kind: 'agent';        step: RunStep; task?: TaskOut; artifacts: ArtifactOut[] }
  | { kind: 'dagnode';      step: RunStep }
  | { kind: 'summary';      artifact: ArtifactOut }
  | { kind: 'answer';       artifact: ArtifactOut };

export type GraphRow = { nodes: GraphNode[]; parallel: boolean };

export function buildGraph(detail: RunDetail, tasks: TaskOut[], artifacts: ArtifactOut[]): GraphRow[] {
  // AppFlow (Graph-mode) steps are identified by a non-empty node_id — see
  // db/103_run_steps_appflow_trace.sql. Orchestrator-mode steps never set it.
  // These two shapes don't mix within one run, so branch once at the top
  // rather than threading a check through every helper below.
  if (detail.steps.some(s => s.node_id)) {
    return buildDagGraph(detail);
  }
  return buildOrchestratorGraph(detail, tasks, artifacts);
}

// buildDagGraph renders an AppFlow run as the sequence of nodes in the order
// they actually started (started_at) — not a true branch/merge layout (no
// edges are drawn). This is deliberately the same "one row at a time, or a
// parallel row for concurrent nodes" model buildOrchestratorGraph already
// uses for parallel agents, reused rather than replaced, per
// docs/APP_CANVAS_DEBUG_PLAN.md's "fix the existing Flow tree, don't build a
// second visualizer" direction. Nodes that started within 1 second of each
// other are grouped into one parallel row — an approximation for fork
// branches, which the trace data (started_at only, no explicit branch/group
// id) can't distinguish more precisely from two nodes that simply started in
// quick succession.
function buildDagGraph(detail: RunDetail): GraphRow[] {
  const rows: GraphRow[] = [];
  rows.push({ nodes: [{ kind: 'user', text: detail.goal || detail.user_message || '' }], parallel: false });
  rows.push({ nodes: [{ kind: 'orchestrator', run: detail }], parallel: false });

  const steps = [...detail.steps].sort((a, b) => new Date(a.started_at).getTime() - new Date(b.started_at).getTime());
  const groupWindowMs = 1000;
  let i = 0;
  while (i < steps.length) {
    const group = [steps[i]];
    const groupStart = new Date(steps[i].started_at).getTime();
    let j = i + 1;
    while (j < steps.length && new Date(steps[j].started_at).getTime() - groupStart < groupWindowMs) {
      group.push(steps[j]);
      j++;
    }
    rows.push({
      nodes: group.map(step => ({ kind: 'dagnode', step } as GraphNode)),
      parallel: group.length > 1,
    });
    i = j;
  }

  return rows;
}

function buildOrchestratorGraph(detail: RunDetail, tasks: TaskOut[], artifacts: ArtifactOut[]): GraphRow[] {
  const rows: GraphRow[] = [];

  // Row 0: user message
  rows.push({ nodes: [{ kind: 'user', text: detail.goal || detail.user_message || '' }], parallel: false });

  // Row 1: orchestrator
  rows.push({ nodes: [{ kind: 'orchestrator', run: detail }], parallel: false });

  // Group steps by iteration — parallel agents share the same iteration
  const byIter = new Map<number, RunStep[]>();
  for (const step of detail.steps) {
    const arr = byIter.get(step.iteration) ?? [];
    arr.push(step);
    byIter.set(step.iteration, arr);
  }

  const taskBySlugIter = new Map<string, TaskOut>();
  for (const t of tasks) {
    if (t.kind === 'delegated' && t.agent_id) {
      const step = detail.steps.find(s => s.agent_slug && t.agent_id);
      if (step) taskBySlugIter.set(`${step.iteration}-${step.agent_slug}`, t);
    }
  }

  const artifactsByTaskId = new Map<string, ArtifactOut[]>();
  for (const a of artifacts) {
    const arr = artifactsByTaskId.get(a.task_id) ?? [];
    arr.push(a);
    artifactsByTaskId.set(a.task_id, arr);
  }

  // Find summary artifacts
  const summaryArtifacts = artifacts.filter(a => a.artifact_id?.startsWith('summary-'));
  const finalAnswer = artifacts.find(a => a.artifact_id === 'final-answer');

  for (const [iter, steps] of Array.from(byIter.entries()).sort((a, b) => a[0] - b[0])) {
    // Iteration label row (only if more than one iteration)
    if (byIter.size > 1) {
      rows.push({ nodes: [{ kind: 'iteration', iteration: iter }], parallel: false });
    }

    // Agent nodes — all steps in this iteration are parallel
    const agentNodes: GraphNode[] = steps.map(step => {
      const task = taskBySlugIter.get(`${iter}-${step.agent_slug}`);
      const taskArtifacts = task ? (artifactsByTaskId.get(task.id) ?? []) : [];
      return { kind: 'agent', step, task, artifacts: taskArtifacts };
    });
    rows.push({ nodes: agentNodes, parallel: agentNodes.length > 1 });

    // Summary after this iteration if exists
    const summary = summaryArtifacts.find(a => {
      const lastStep = steps[steps.length - 1];
      return lastStep && new Date(a.created_at) > new Date(lastStep.started_at);
    });
    if (summary && !rows.some(r => r.nodes.some(n => n.kind === 'summary' && (n as { kind: 'summary'; artifact: ArtifactOut }).artifact.id === summary.id))) {
      rows.push({ nodes: [{ kind: 'summary', artifact: summary }], parallel: false });
    }
  }

  // Final answer node
  if (finalAnswer) {
    rows.push({ nodes: [{ kind: 'answer', artifact: finalAnswer }], parallel: false });
  }

  return rows;
}
