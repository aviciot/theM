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
  | { kind: 'summary';      artifact: ArtifactOut }
  | { kind: 'answer';       artifact: ArtifactOut };

export type GraphRow = { nodes: GraphNode[]; parallel: boolean };

export function buildGraph(detail: RunDetail, tasks: TaskOut[], artifacts: ArtifactOut[]): GraphRow[] {
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
