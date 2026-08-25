// eslint-disable-next-line @typescript-eslint/no-require-imports, @typescript-eslint/no-explicit-any
const dagre: any = (typeof window !== 'undefined' ? require('dagre') : null);

import { Position, type Node, type Edge } from '@xyflow/react';
import type {
  AppDefinitionDoc,
  ComponentInstance,
  EPInstance,
  ConnectionDef,
  DefinitionRef,
  ComponentDefinitionSummary,
} from '@/lib/api';
import type {
  OrchNodeData,
  AgentNodeData,
  MwNodeData,
  EpNodeData,
  EntryPointData,
  EntryPointType,
  OrchestratorData,
  AgentData,
  Application,
  Agent,
  AppOrchestratorOut,
} from '../types';
import { EP_META, NODE_WIDTH, NODE_HEIGHT, EDGE_STYLE } from '../constants';

// ── Misc helpers ──────────────────────────────────────────────────────────────
export function makeId(): string {
  return `node_${Date.now()}_${Math.random().toString(36).slice(2, 7)}`;
}

export function fallbackCopy(text: string): void {
  const el = document.createElement('textarea');
  el.value = text;
  el.style.cssText = 'position:fixed;top:-9999px;left:-9999px;opacity:0';
  document.body.appendChild(el);
  el.select();
  document.execCommand('copy');
  document.body.removeChild(el);
}

export function trunc(s: string | null | undefined, n = 120): string {
  if (!s) return '—';
  return s.length > n ? s.slice(0, n) + '…' : s;
}

export function getBridgeWs(): string {
  if (typeof window === 'undefined') return '';
  if (process.env.NEXT_PUBLIC_BRIDGE_WS_URL) return process.env.NEXT_PUBLIC_BRIDGE_WS_URL;
  const proto = window.location.protocol === 'https:' ? 'wss' : 'ws';
  return `${proto}://${window.location.host}`;
}

export function agentIconForLibrary(a: { slug?: string; icon?: string | null }): string {
  if (a.icon) return a.icon;
  const slug = (a.slug ?? '').toLowerCase();
  if (slug.includes('vision') || slug.includes('image') || slug.includes('ocr') || slug.includes('photo')) return 'image_search';
  if (slug.includes('security') || slug.includes('scan') || slug.includes('audit')) return 'security';
  if (slug.includes('code') || slug.includes('dev') || slug.includes('engineer')) return 'code';
  if (slug.includes('search') || slug.includes('web') || slug.includes('browse')) return 'travel_explore';
  if (slug.includes('doc') || slug.includes('write') || slug.includes('text') || slug.includes('summar')) return 'description';
  if (slug.includes('data') || slug.includes('analyt') || slug.includes('sql') || slug.includes('db')) return 'table_chart';
  if (slug.includes('email') || slug.includes('mail') || slug.includes('gmail')) return 'email';
  if (slug.includes('slack') || slug.includes('chat') || slug.includes('message')) return 'chat';
  if (slug.includes('judge') || slug.includes('eval') || slug.includes('review')) return 'rate_review';
  if (slug.includes('logic') || slug.includes('reason') || slug.includes('think')) return 'psychology';
  if (slug.includes('creat') || slug.includes('design') || slug.includes('art')) return 'palette';
  if (slug.includes('voice') || slug.includes('audio') || slug.includes('speech') || slug.includes('tts')) return 'record_voice_over';
  if (slug.includes('echo') || slug.includes('test') || slug.includes('mock')) return 'bug_report';
  if (slug.includes('slow') || slug.includes('queue') || slug.includes('batch')) return 'hourglass_top';
  if (slug.includes('stream')) return 'stream';
  if (slug.includes('a2a') || slug.includes('robot')) return 'robot_2';
  if (slug.includes('evidence') || slug.includes('fact') || slug.includes('verify')) return 'fact_check';
  return 'smart_toy';
}

// ── Dagre auto-layout ─────────────────────────────────────────────────────────
export function applyDagreLayout(nodes: Node[], edges: Edge[], dir: 'TB' | 'LR' = 'TB'): Node[] {
  const g = new dagre.graphlib.Graph();
  g.setDefaultEdgeLabel(() => ({}));
  g.setGraph({ rankdir: dir, nodesep: 60, ranksep: 100, marginx: 60, marginy: 60 });

  nodes.forEach(n => g.setNode(n.id, { width: NODE_WIDTH, height: NODE_HEIGHT }));
  edges.forEach(e => g.setEdge(e.source, e.target));

  dagre.layout(g);

  const sourcePos = dir === 'LR' ? Position.Right : Position.Bottom;
  const targetPos = dir === 'LR' ? Position.Left  : Position.Top;
  return nodes.map(n => {
    const pos = g.node(n.id);
    return { ...n, position: { x: pos.x - NODE_WIDTH / 2, y: pos.y - NODE_HEIGHT / 2 }, sourcePosition: sourcePos, targetPosition: targetPos };
  });
}

// ── Canvas V2 serialization helpers ──────────────────────────────────────────
function sanitize(s: string): string {
  return s.toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/_+/g, '_').replace(/^_|_$/g, '').slice(0, 20);
}

export function genInstanceId(kind: 'orchestrator' | 'agent' | 'middleware' | 'ep', defName: string | undefined, existing: Set<string>): string {
  let base: string;
  if (kind === 'orchestrator') base = 'orch';
  else if (kind === 'ep') base = 'ep_' + sanitize(defName ?? 'ep');
  else if (kind === 'agent') base = 'agent_' + sanitize(defName ?? 'agent');
  else base = 'mw_' + sanitize(defName ?? 'mw');
  let n = 1;
  while (existing.has(`${base}_${n}`)) n++;
  return `${base}_${n}`;
}

export function canvasToDoc(nodes: Node[], edges: Edge[], name?: string): AppDefinitionDoc {
  const nodeTypeById = new Map(nodes.map(n => [n.id, n.type]));
  const rootByEp = new Map<string, string>();
  edges.forEach(e => {
    if (nodeTypeById.get(e.source) === 'entryPoint' && nodeTypeById.get(e.target) === 'orchestrator') {
      rootByEp.set(e.source, e.target);
    }
  });
  const components: ComponentInstance[] = [];
  const entry_points: EPInstance[] = [];
  const connections: ConnectionDef[] = [];
  nodes.forEach(n => {
    if (n.type === 'orchestrator') {
      const d = n.data as unknown as OrchNodeData;
      components.push({ instance_id: n.id, name: n.id, definition_ref: d.definition_ref, definition_id: d.definition_id, config: { ...d.config, display_name: d.display_name } });
    } else if (n.type === 'agent') {
      const d = n.data as unknown as AgentNodeData;
      const comp: ComponentInstance = { instance_id: n.id, definition_ref: d.definition_ref, definition_id: d.definition_id, config: d.config };
      if (d.secret_bindings && Object.keys(d.secret_bindings).length) comp.secret_bindings = d.secret_bindings;
      components.push(comp);
    } else if (n.type === 'middleware') {
      const d = n.data as unknown as MwNodeData;
      components.push({ instance_id: n.id, definition_ref: d.definition_ref, definition_id: d.definition_id, config: d.config });
    } else if (n.type === 'entryPoint') {
      const d = n.data as unknown as EpNodeData;
      entry_points.push({ instance_id: n.id, slug: d.slug, protocol: d.protocol, root: rootByEp.get(n.id) ?? '', config: d.config ?? {} });
    }
  });
  edges.forEach(e => {
    const srcType = nodeTypeById.get(e.source);
    const tgtType = nodeTypeById.get(e.target);
    if (srcType === 'entryPoint' && tgtType === 'orchestrator') return;
    if (srcType === 'orchestrator' && tgtType === 'agent') connections.push({ source: e.source, target: e.target, type: 'tool' });
    if (srcType === 'orchestrator' && tgtType === 'orchestrator') connections.push({ source: e.source, target: e.target, type: 'delegation' });
  });
  return { schema_version: 2 as const, name, components, entry_points, connections };
}

export function docToCanvas(
  doc: AppDefinitionDoc,
  componentDefs: ComponentDefinitionSummary[],
  layout: Record<string, { x: number; y: number }>,
  agentIconBySlug?: Map<string, string>,
): { nodes: Node[]; edges: Edge[] } {
  const defById = new Map(componentDefs.map(cd => [cd.id, cd]));
  const refKey = (r: DefinitionRef) => `${r.kind}:${r.namespace}:${r.name}:${r.version}`;
  const defByRef = new Map(componentDefs.map(cd => [refKey({ kind: cd.kind, namespace: cd.namespace, name: cd.name, version: cd.version }), cd]));
  const nodes: Node[] = [];
  const edges: Edge[] = [];
  (doc.components ?? []).forEach(c => {
    const cd = defById.get(c.definition_id ?? '') ?? defByRef.get(refKey(c.definition_ref));
    const pos = layout[c.instance_id] ?? { x: 0, y: 0 };
    if (c.definition_ref.kind === 'orchestrator') {
      nodes.push({ id: c.instance_id, type: 'orchestrator', position: pos, data: { _kind: 'orchestrator', instance_id: c.instance_id, display_name: (c.config.display_name as string) ?? cd?.display_name ?? c.instance_id, definition_ref: c.definition_ref, definition_id: c.definition_id, config: c.config } as unknown as Record<string, unknown> });
    } else if (c.definition_ref.kind === 'agent') {
      const agentIcon = agentIconBySlug?.get(c.definition_ref.name);
      nodes.push({ id: c.instance_id, type: 'agent', position: pos, data: { _kind: 'agent', instance_id: c.instance_id, display_name: cd?.display_name ?? c.instance_id, description: cd?.description ?? '', definition_ref: c.definition_ref, definition_id: c.definition_id, config: c.config, secret_bindings: c.secret_bindings, icon: agentIcon } as unknown as Record<string, unknown> });
    } else if (c.definition_ref.kind === 'middleware') {
      nodes.push({ id: c.instance_id, type: 'middleware', position: pos, data: { _kind: 'middleware', instance_id: c.instance_id, display_name: cd?.display_name ?? c.instance_id, definition_ref: c.definition_ref, definition_id: c.definition_id, config: c.config } as unknown as Record<string, unknown> });
    }
  });
  (doc.entry_points ?? []).forEach(ep => {
    const pos = layout[ep.instance_id] ?? { x: 0, y: 0 };
    nodes.push({ id: ep.instance_id, type: 'entryPoint', position: pos, data: { _kind: 'ep', instance_id: ep.instance_id, slug: ep.slug, protocol: ep.protocol, label: EP_META[ep.protocol]?.title ?? ep.protocol, config: ep.config ?? {} } as unknown as Record<string, unknown> });
    if (ep.root) edges.push({ id: `e_${ep.instance_id}_${ep.root}`, source: ep.instance_id, target: ep.root, type: 'default' });
  });
  (doc.connections ?? []).forEach(conn => {
    if (conn.type === 'tool' || conn.type === 'delegation') {
      edges.push({ id: `e_${conn.source}_${conn.target}`, source: conn.source, target: conn.target, type: 'default' });
    }
  });
  if (Object.keys(layout).length === 0 && nodes.length > 0) {
    const laid = applyDagreLayout(nodes, edges);
    laid.forEach((ln, i) => { nodes[i].position = ln.position; });
  }
  return { nodes, edges };
}

// ── Legacy canvas builder (buildNodesFromApp) ────────────────────────────────
export function buildNodesFromApp(
  app: Application,
  agents: Agent[],
): { nodes: Node[]; edges: Edge[] } {
  const layout = (app.canvas?.layout ?? {}) as Record<string, { x: number; y: number }>;

  const nodes: Node[] = [];
  const edges: Edge[] = [];

  const aoById = new Map<string, AppOrchestratorOut>();
  (app.app_orchestrators ?? []).forEach(ao => aoById.set(ao.id, ao as unknown as AppOrchestratorOut));
  (app.entry_points ?? []).forEach(ep => {
    if (ep.app_orchestrator) aoById.set(ep.app_orchestrator.id, ep.app_orchestrator);
  });

  const emittedOrchIds = new Set<string>();
  const emittedAgentNodeIds = new Set<string>();

  const pos = (nodeId: string, legacyKey?: string) =>
    layout[nodeId] ?? (legacyKey ? layout[legacyKey] : null) ?? null;

  (app.entry_points ?? []).forEach((ep, idx) => {
    const epId = `ep_${ep.slug}`;
    nodes.push({
      id: epId, type: 'entryPoint',
      position: pos(epId, `ep:${ep.slug}`) ?? { x: 150 + idx * 240, y: 60 },
      data: {
        label: app.name,
        epType: (ep.entry_point_type as EntryPointType) ?? 'websocket',
        accessMode: ((ep.access_policy as any)?.mode ?? 'token') as 'token' | 'public',
        slug: ep.slug,
        appName: app.name,
        convTokenLimit: ep.conversation_token_limit != null ? String(ep.conversation_token_limit) : '',
        maxConcurrentSessions: ep.max_concurrent_sessions != null ? String(ep.max_concurrent_sessions) : '',
        queueTimeout: ep.queue_timeout_seconds != null ? String(ep.queue_timeout_seconds) : '',
        queueMessage: ep.queue_message ?? '',
        _epId: ep.id,
      } satisfies EntryPointData,
    });

    const aoId = ep.app_orchestrator_id ?? ep.app_orchestrator?.id;
    if (aoId) {
      const orchNodeId = `orch_${aoId}`;
      edges.push({ id: `e_ep_orch_${ep.slug}`, source: epId, target: orchNodeId, animated: true, style: EDGE_STYLE });

      if (!emittedOrchIds.has(aoId)) {
        emittedOrchIds.add(aoId);
        const ao = aoById.get(aoId);
        if (ao) {
          nodes.push({
            id: orchNodeId, type: 'orchestrator',
            position: pos(orchNodeId, `orch:${aoId}`) ?? (ao.node_id ? pos(ao.node_id, `orch:${ao.node_id}`) : null) ?? { x: 250, y: 220 },
            data: {
              appOrchestratorId: ao.id,
              orchestratorId: ao.id,
              name: ao.name,
              displayName: ao.display_name || ao.name,
              model: ao.llm_model,
              maxParallelTools: ao.max_parallel_tools,
              systemPrompt: ao.system_prompt,
              allowedAgentIds: ao.allowed_agent_ids,
              mcpServers: ao.mcp_servers ?? [],
              llmProvider: ao.llm_provider,
              llmModel: ao.llm_model,
              maxIterations: ao.max_iterations,
              historyWindow: ao.history_window ?? 20,
              delegatable: ao.delegatable,
              kind: ao.kind,
              budgetTokens: ao.budget_tokens,
              transcriptionProvider: ao.transcription_provider ?? null,
              transcriptionModel: ao.transcription_model ?? null,
              transcriptionApiKey: null,
              ttsProvider: ao.tts_provider ?? null,
              ttsVoice: ao.tts_voice ?? null,
              ttsApiKey: null,
            } as OrchestratorData,
          });

          const allowedAgents = agents.filter(a => ao.allowed_agent_ids.includes(a.id));
          const spread = Math.max(allowedAgents.length * 180, 400);
          const startX = 300 - spread / 2 + 90;
          allowedAgents.forEach((agent, i) => {
            const agentNodeId = `agent_${agent.id}`;
            if (!emittedAgentNodeIds.has(agentNodeId)) {
              emittedAgentNodeIds.add(agentNodeId);
              nodes.push({
                id: agentNodeId, type: 'agent',
                position: pos(agentNodeId, `agent:${agent.id}`) ?? { x: startX + i * 190, y: 420 },
                data: {
                  agentId: agent.id,
                  name: agent.slug,
                  displayName: agent.display_name,
                  description: agent.description,
                  transport: agent.transport,
                  endpointUrl: agent.endpoint_url,
                  tags: agent.tags ?? [],
                  icon: agent.icon || agentIconForLibrary(agent),
                } satisfies AgentData,
              });
            }
            edges.push({ id: `e_orch_agent_${aoId}_${agent.id}`, source: orchNodeId, target: agentNodeId, animated: true, style: EDGE_STYLE });
          });
        }
      }
    }
  });

  const hasLayout = Object.keys(layout).length > 0;
  if (!hasLayout) {
    const laid = applyDagreLayout(nodes, edges);
    return { nodes: laid, edges };
  }
  return { nodes, edges };
}
