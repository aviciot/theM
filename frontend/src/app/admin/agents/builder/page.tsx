'use client';
import { useCallback, useEffect, useState, type MouseEvent, type DragEvent } from 'react';
import { useRouter, useSearchParams } from 'next/navigation';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import {
  themApi,
  type AgentDefinitionDoc,
  type AgentSkillDoc,
  type AgentStepDoc,
  type AgentCredentialSlot,
} from '@/lib/api';
import {
  ReactFlow,
  ReactFlowProvider,
  Background,
  BackgroundVariant,
  Controls,
  addEdge,
  useNodesState,
  useEdgesState,
  type Node,
  type Edge,
  type Connection,
  type NodeTypes,
  Handle,
  Position,
  useReactFlow,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';

// ── Design tokens ─────────────────────────────────────────────────────────────
const C = {
  bg: 'var(--tm-bg)',
  surface: 'var(--tm-panel)',
  cyan: '#00f0ff',
  cyanBg: 'rgba(0,240,255,0.05)',
  cyanBorder: 'rgba(0,240,255,0.4)',
  purple: '#d0bcff',
  purpleBg: 'rgba(87,27,193,0.1)',
  purpleBorder: '#d0bcff',
  green: '#4ade80',
  greenBg: 'rgba(74,222,128,0.05)',
  greenBorder: 'rgba(74,222,128,0.3)',
  amber: '#f59e0b',
  amberBg: 'rgba(245,158,11,0.05)',
  amberBorder: 'rgba(245,158,11,0.3)',
  indigo: '#6366f1',
  indigoBg: 'rgba(99,102,241,0.1)',
  indigoBorder: 'rgba(99,102,241,0.5)',
  text: 'var(--tm-card-text)',
  textMuted: 'var(--tm-card-text-muted)',
  outline: 'var(--tm-canvas-border)',
};

// ── Node data types ────────────────────────────────────────────────────────────

interface AgentRootData {
  display_name: string;
  description: string;
  version: string;
  credential_slots: AgentCredentialSlot[];
}

interface SkillData {
  skill_id: string;
  name: string;
  description: string;
  tags: string[];
  input_modes: string[];
  output_modes: string[];
  examples: string[];
}

interface StepData {
  step_id: string;
  step_type: string;
  label: string;
  config: Record<string, unknown>;
}

// ── Shared panel styles (match application canvas) ────────────────────────────
const labelStyle: React.CSSProperties = {
  fontSize: 11, color: 'var(--tm-card-text-subtle)', marginBottom: 4, display: 'block', fontWeight: 700,
};
const inputStyle: React.CSSProperties = {
  width: '100%', background: 'transparent',
  border: '1px solid var(--tm-canvas-border)', color: '#fff',
  padding: '6px', borderRadius: '4px', fontSize: '13px', boxSizing: 'border-box',
};
const textareaStyle: React.CSSProperties = {
  ...inputStyle, resize: 'vertical' as const, fontFamily: 'inherit',
};
const selectStyle: React.CSSProperties = { ...inputStyle };
const fieldGap: React.CSSProperties = { marginTop: '12px' };
const hint: React.CSSProperties = { fontSize: 10, color: '#64748b', marginLeft: 4 };
const ctxItemStyle: React.CSSProperties = {
  display: 'block', width: '100%', textAlign: 'left',
  background: 'transparent', border: 'none', color: '#e2e8f0',
  padding: '7px 12px', borderRadius: '5px', cursor: 'pointer',
  fontSize: '13px', transition: 'background 0.1s',
};

// ── Available LLM models (hardcoded, same as application canvas) ──────────────
const LLM_MODELS: Record<string, string[]> = {
  anthropic: ['claude-opus-4-8', 'claude-sonnet-4-6', 'claude-haiku-4-5-20251001'],
};

// ── Node components (must be outside the render component) ───────────────────

function AgentRootNode({ data }: { data: AgentRootData; id: string }) {
  return (
    <div style={{ background: 'transparent', border: 'none', padding: '8px', minWidth: '120px', textAlign: 'center' }}>
      <Handle type="source" position={Position.Bottom} style={{ background: C.cyan }} />
      <div style={{ fontSize: '42px', textAlign: 'center', lineHeight: 1 }}>🤖</div>
      <div style={{ color: '#fff', fontWeight: 700, fontSize: '13px', textAlign: 'center', marginTop: '6px' }}>{data.display_name || 'Unnamed Agent'}</div>
      {data.credential_slots.length > 0 && (
        <div style={{ marginTop: '6px', fontSize: '11px', color: C.amber, textAlign: 'center' }}>
          {data.credential_slots.length} credential slot{data.credential_slots.length !== 1 ? 's' : ''}
        </div>
      )}
    </div>
  );
}

function SkillNode({ data }: { data: SkillData; id: string }) {
  return (
    <div style={{ background: 'transparent', border: 'none', padding: '8px', minWidth: '100px', textAlign: 'center' }}>
      <Handle type="target" position={Position.Top} style={{ background: C.purple }} />
      <Handle type="source" position={Position.Bottom} style={{ background: C.purple }} />
      <div style={{ fontSize: '36px', lineHeight: 1 }}>⚡</div>
      <div style={{ color: '#fff', fontWeight: 700, fontSize: '12px', marginTop: '6px' }}>{data.name || 'Skill'}</div>
    </div>
  );
}

const STEP_META: Record<string, { bg: string; border: string; emoji: string; label: string }> = {
  input:      { bg: C.greenBg,  border: C.greenBorder,  emoji: '📥', label: 'Input' },
  response:   { bg: C.cyanBg,   border: C.cyanBorder,   emoji: '📤', label: 'Response' },
  llm:        { bg: C.purpleBg, border: C.purpleBorder, emoji: '🧠', label: 'LLM' },
  http:       { bg: C.amberBg,  border: C.amberBorder,  emoji: '🌐', label: 'HTTP' },
  transform:  { bg: C.indigoBg, border: C.indigoBorder, emoji: '⚙️',  label: 'Transform' },
  branch:     { bg: C.amberBg,  border: C.amberBorder,  emoji: '🔀', label: 'Branch' },
  loop:       { bg: C.amberBg,  border: C.amberBorder,  emoji: '🔁', label: 'Loop' },
  parallel:   { bg: C.purpleBg, border: C.purpleBorder, emoji: '⚡', label: 'Parallel' },
  a2a_call:   { bg: C.cyanBg,   border: C.cyanBorder,   emoji: '🤝', label: 'A2A Call' },
  human_wait: { bg: C.greenBg,  border: C.greenBorder,  emoji: '⏸️',  label: 'Human Wait' },
  stream_out: { bg: C.cyanBg,   border: C.cyanBorder,   emoji: '📡', label: 'Stream Out' },
};

function StepNode({ data }: { data: StepData; id: string }) {
  const meta = STEP_META[data.step_type] ?? { bg: C.indigoBg, border: C.indigoBorder, emoji: '🔧', label: data.step_type };
  const cfg = data.config ?? {};
  let sub = '';
  if (data.step_type === 'input') {
    const bindings = cfg.bindings as Record<string, string> | undefined;
    sub = bindings?.text ? `→ ${bindings.text}` : '→ input';
  } else if (data.step_type === 'llm') {
    sub = `→ ${(cfg.output_var as string) || 'output'}`;
  } else if (data.step_type === 'transform') {
    const keys = Object.keys((cfg.expressions as Record<string, string> | undefined) ?? {});
    sub = keys.length ? `→ ${keys.join(', ')}` : '→ vars';
  } else if (data.step_type === 'response') {
    sub = `from ${(cfg.from_var as string) || 'output'}`;
  } else if (data.step_type === 'http') {
    sub = (cfg.url_template as string) ? (cfg.url_template as string).replace(/^https?:\/\//, '').slice(0, 22) : 'url not set';
  }
  return (
    <div style={{ background: 'transparent', border: 'none', padding: '8px', minWidth: '80px', textAlign: 'center' }}>
      <Handle type="target" position={Position.Top} style={{ background: meta.border }} />
      <Handle type="source" position={Position.Bottom} style={{ background: meta.border }} />
      <div style={{ fontSize: '32px', lineHeight: 1 }}>{meta.emoji}</div>
      <div style={{ color: '#fff', fontWeight: 700, fontSize: '11px', marginTop: '5px' }}>{meta.label}</div>
      {sub && <div style={{ fontSize: '10px', color: meta.border, opacity: 0.9, marginTop: 2 }}>{sub}</div>}
    </div>
  );
}

// nodeTypes MUST be defined outside the component for stable references.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const nodeTypes: NodeTypes = {
  agentRoot: AgentRootNode as any,
  skill:     SkillNode     as any,
  step:      StepNode      as any,
};

// ── Step type palette ─────────────────────────────────────────────────────────

const STEP_TYPES: { type: AgentStepDoc['type']; label: string }[] = [
  { type: 'input',      label: 'Input' },
  { type: 'llm',        label: 'LLM' },
  { type: 'http',       label: 'HTTP Tool' },
  { type: 'transform',  label: 'Transform' },
  { type: 'response',   label: 'Response' },
  { type: 'branch',     label: 'Branch' },
  { type: 'loop',       label: 'Loop' },
  { type: 'parallel',   label: 'Parallel' },
  { type: 'a2a_call',   label: 'A2A Call' },
  { type: 'human_wait', label: 'Human Wait' },
  { type: 'stream_out', label: 'Stream Out' },
];

function genUUID(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID();
  }
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, c => {
    const r = Math.random() * 16 | 0;
    return (c === 'x' ? r : (r & 0x3 | 0x8)).toString(16);
  });
}

// ── Canvas inner (uses ReactFlow hooks — must be inside ReactFlowProvider) ───

// Step type → which config field receives the auto-filled upstream variable reference.
const STEP_INPUT_FIELD: Record<string, string> = {
  llm: 'user_prompt',
  http: 'url_template',
  transform: 'expression',
  response: 'from_var',
};
// Step types that accept only one incoming edge.
const SINGLE_INPUT_TYPES = new Set(['llm', 'transform', 'response']);

function CanvasInner() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const defId = searchParams.get('id');
  const { screenToFlowPosition } = useReactFlow();

  // View state: 'agent' = top-level, 'skill' = pipeline for a skill
  const [activeView, setActiveView] = useState<'agent' | 'skill'>('agent');
  const [activeSkillId, setActiveSkillId] = useState<string | null>(null);

  // Agent-level nodes/edges — pre-seed AGENT ROOT for new drafts
  const initialAgentNodes: Node[] = defId ? [] : [{
    id: 'agent-root',
    type: 'agentRoot',
    position: { x: 300, y: 80 },
    data: { display_name: 'My Agent', description: '', version: '1.0.0', credential_slots: [] },
  }];
  const [agentNodes, setAgentNodes, onAgentNodesChange] = useNodesState<Node>(initialAgentNodes);
  const [agentEdges, setAgentEdges, onAgentEdgesChange] = useEdgesState<Edge>([]);

  // Per-skill pipeline state
  const [skillPipelines, setSkillPipelines] = useState<Record<string, { nodes: Node[]; edges: Edge[] }>>({});

  // Agent metadata
  const [agentSlug, setAgentSlug] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [description, setDescription] = useState('');
  const [version, setVersion] = useState('1.0.0');
  const [credentialSlots, setCredentialSlots] = useState<AgentCredentialSlot[]>([]);

  // UI state
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [validating, setValidating] = useState(false);
  const [publishing, setPublishing] = useState(false);
  const [saveError, setSaveError] = useState('');
  const [loadError, setLoadError] = useState('');
  const [publishError, setPublishError] = useState('');
  const [publishedRevision, setPublishedRevision] = useState<number | null>(null);
  const [dirty, setDirty] = useState(false);

  // Properties panel
  const [selectedNode, setSelectedNode] = useState<Node | null>(null);

  // Pipeline nodes/edges for the active skill view
  const pipelineNodes = activeSkillId ? (skillPipelines[activeSkillId]?.nodes ?? []) : [];
  const pipelineEdges = activeSkillId ? (skillPipelines[activeSkillId]?.edges ?? []) : [];
  const [localPipeNodes, setLocalPipeNodes, onPipeNodesChange] = useNodesState<Node>(pipelineNodes);
  const [localPipeEdges, setLocalPipeEdges, onPipeEdgesChange] = useEdgesState<Edge>(pipelineEdges);

  // Sync pipeline state when switching skills
  useEffect(() => {
    if (activeSkillId) {
      const state = skillPipelines[activeSkillId] ?? { nodes: [], edges: [] };
      setLocalPipeNodes(state.nodes);
      setLocalPipeEdges(state.edges);
    }
  }, [activeSkillId]); // eslint-disable-line react-hooks/exhaustive-deps

  // Save pipeline state when navigating away from a skill
  const savePipelineState = useCallback(() => {
    if (activeSkillId) {
      setSkillPipelines(prev => ({
        ...prev,
        [activeSkillId]: { nodes: localPipeNodes, edges: localPipeEdges },
      }));
    }
  }, [activeSkillId, localPipeNodes, localPipeEdges]);

  // Load existing definition if ?id= is present
  useEffect(() => {
    if (!defId) return;
    themApi.getAgentDefinition(defId).then(resp => {
      const doc = resp.definition;
      setAgentSlug(doc.agent_slug);
      setDisplayName(doc.agent_root.display_name);
      setDescription(doc.agent_root.description ?? '');
      setVersion(doc.agent_root.version ?? '1.0.0');
      setCredentialSlots(doc.agent_root.credential_slots ?? []);
      loadDefinitionDoc(doc);
    }).catch(e => {
      setLoadError('Failed to load definition: ' + String(e));
    });
  }, [defId]); // eslint-disable-line react-hooks/exhaustive-deps

  function loadDefinitionDoc(doc: AgentDefinitionDoc) {
    // Build agent-level nodes
    const rootNode: Node = {
      id: 'agent-root',
      type: 'agentRoot',
      position: { x: 300, y: 80 },
      data: {
        display_name: doc.agent_root.display_name,
        description: doc.agent_root.description ?? '',
        version: doc.agent_root.version ?? '1.0.0',
        credential_slots: doc.agent_root.credential_slots ?? [],
      },
    };
    const skillNodes: Node[] = doc.skills.map((sk, i) => ({
      id: `skill-${sk.skill_id}`,
      type: 'skill',
      position: sk.position ?? { x: 150 + i * 220, y: 250 },
      data: {
        skill_id: sk.skill_id,
        name: sk.name,
        description: sk.description ?? '',
        tags: sk.tags ?? [],
        input_modes: sk.input_modes ?? ['text/plain'],
        output_modes: sk.output_modes ?? ['text/plain'],
        examples: sk.examples ?? [],
      },
    }));
    const skillEdges: Edge[] = doc.skills.map(sk => ({
      id: `root-to-${sk.skill_id}`,
      source: 'agent-root',
      target: `skill-${sk.skill_id}`,
    }));
    setAgentNodes([rootNode, ...skillNodes]);
    setAgentEdges(skillEdges);

    // Build per-skill pipeline state
    const pipelines: Record<string, { nodes: Node[]; edges: Edge[] }> = {};
    for (const sk of doc.skills) {
      const stepNodes: Node[] = (sk.steps ?? []).map((step, si) => ({
        id: `step-${step.id}`,
        type: 'step',
        position: step.position ?? { x: 200, y: 80 + si * 120 },
        data: { step_id: step.id, step_type: step.type, label: step.type, config: (step.config as Record<string, unknown>) ?? {} },
      }));
      const stepEdges: Edge[] = [];
      for (const step of (sk.steps ?? [])) {
        for (const nextId of (step.next ?? [])) {
          stepEdges.push({ id: `${step.id}-to-${nextId}`, source: `step-${step.id}`, target: `step-${nextId}` });
        }
      }
      pipelines[sk.skill_id] = { nodes: stepNodes, edges: stepEdges };
    }
    setSkillPipelines(pipelines);
  }

  function buildDefinitionDoc(): AgentDefinitionDoc {
    // Find the root node data
    const rootNodeData = agentNodes.find(n => n.id === 'agent-root')?.data as unknown as AgentRootData | undefined;
    const dn = rootNodeData?.display_name ?? displayName;
    const desc = rootNodeData?.description ?? description;
    const ver = rootNodeData?.version ?? version;
    const slots = rootNodeData?.credential_slots ?? credentialSlots;

    const skills: AgentSkillDoc[] = agentNodes
      .filter(n => n.type === 'skill')
      .map(n => {
        const sd = n.data as unknown as SkillData;
        const pipeline = skillPipelines[sd.skill_id] ?? { nodes: [], edges: [] };
        const steps: AgentStepDoc[] = pipeline.nodes.map(sn => {
          const stepd = sn.data as unknown as StepData;
          const outEdges = pipeline.edges.filter(e => e.source === sn.id);
          return {
            id: stepd.step_id,
            type: stepd.step_type as AgentStepDoc['type'],
            config: stepd.config ?? {},
            next: outEdges.map(e => (e.target as string).replace('step-', '')),
            position: sn.position,
          };
        });
        return {
          skill_id: sd.skill_id,
          name: sd.name,
          description: sd.description ?? '',
          tags: sd.tags ?? [],
          input_modes: sd.input_modes ?? ['text/plain'],
          output_modes: sd.output_modes ?? ['text/plain'],
          examples: sd.examples ?? [],
          input_schema: {},
          output_schema: {},
          steps,
          position: n.position,
        };
      });

    return {
      schema_version: 1,
      agent_slug: agentSlug,
      agent_root: {
        display_name: dn,
        description: desc,
        version: ver,
        capabilities: { streaming: false, push_notifications: false },
        credential_slots: slots,
      },
      skills,
    };
  }

  async function handleSave() {
    if (!agentSlug.trim()) {
      setSaveError('Agent slug is required — fill in the slug field in the toolbar before saving.');
      return;
    }
    setSaving(true);
    setSaveError('');
    savePipelineState();
    try {
      const doc = buildDefinitionDoc();
      if (defId) {
        await themApi.updateAgentDefinition(defId, { definition: doc });
        setDirty(false);
      } else {
        const result = await themApi.createAgentDefinition({ agent_slug: agentSlug, definition: doc });
        router.replace(`/admin/agents/builder?id=${result.id}`);
        setDirty(false);
      }
    } catch (e) {
      setSaveError(String(e));
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete() {
    if (!defId || !confirm('Delete this draft agent definition?')) return;
    setDeleting(true);
    try {
      await themApi.deleteAgentDefinition(defId);
      router.push('/admin/agents');
    } catch (e) {
      setSaveError(String(e));
    } finally {
      setDeleting(false);
    }
  }

  async function handleValidate() {
    if (!defId) return;
    setValidating(true);
    setPublishError('');
    try {
      await themApi.validateAgentDefinition(defId);
      alert('Validation passed — definition is ready to publish.');
    } catch (e: unknown) {
      const body = (e as { response?: { errors?: { message: string }[] } })?.response;
      const msgs = body?.errors?.map((err: { message: string }) => err.message).join('\n');
      setPublishError(msgs ?? String(e));
    } finally {
      setValidating(false);
    }
  }

  async function handlePublish() {
    if (!defId || !confirm('Publish this agent definition? This creates a runtime agent entry.')) return;
    setPublishing(true);
    setPublishError('');
    try {
      const result = await themApi.publishAgentDefinition(defId);
      setPublishedRevision(result.revision);
      setDirty(false);
    } catch (e: unknown) {
      const body = (e as { response?: { errors?: { message: string }[] } })?.response;
      const msgs = body?.errors?.map((err: { message: string }) => err.message).join('\n');
      setPublishError(msgs ?? String(e));
    } finally {
      setPublishing(false);
    }
  }

  function handleBack() {
    savePipelineState();
    if (activeView === 'skill') {
      setActiveView('agent');
      setActiveSkillId(null);
      setSelectedNode(null);
    } else {
      router.push('/admin/agents');
    }
  }

  function makeDefaultPipeline(): { nodes: Node[]; edges: Edge[] } {
    const inputId = genUUID();
    const responseId = genUUID();
    return {
      nodes: [
        { id: `step-${inputId}`,    type: 'step', position: { x: 160, y: 60  }, data: { step_id: inputId,    step_type: 'input',    label: 'Input',    config: {} } },
        { id: `step-${responseId}`, type: 'step', position: { x: 160, y: 280 }, data: { step_id: responseId, step_type: 'response', label: 'Response', config: {} } },
      ],
      edges: [],
    };
  }

  function addSkill() {
    const sid = genUUID();
    const newNode: Node = {
      id: `skill-${sid}`,
      type: 'skill',
      position: { x: 150 + agentNodes.filter(n => n.type === 'skill').length * 220, y: 250 },
      data: { skill_id: sid, name: 'New Skill', description: '', tags: [], input_modes: ['text/plain'], output_modes: ['text/plain'], examples: [] },
    };
    setAgentNodes(prev => [...prev, newNode]);
    if (agentNodes.find(n => n.id === 'agent-root')) {
      setAgentEdges(prev => [...prev, { id: `root-to-${sid}`, source: 'agent-root', target: `skill-${sid}` }]);
    }
    setSkillPipelines(prev => ({ ...prev, [sid]: makeDefaultPipeline() }));
    setDirty(true);
  }

  function addStepToActivePipeline(type: AgentStepDoc['type']) {
    if (!activeSkillId) return;
    const stepId = genUUID();
    const newNode: Node = {
      id: `step-${stepId}`,
      type: 'step',
      position: screenToFlowPosition({ x: 300, y: 200 }),
      data: { step_id: stepId, step_type: type, label: type, config: {} },
    };
    setLocalPipeNodes(prev => [...prev, newNode]);
    setDirty(true);
  }

  function onAgentNodeDoubleClick(_: MouseEvent, node: Node) {
    if (node.type === 'skill') {
      savePipelineState();
      const sd = node.data as unknown as SkillData;
      setActiveSkillId(sd.skill_id);
      setActiveView('skill');
      setSelectedNode(null);
    } else {
      setSelectedNode(node);
    }
  }

  function onPipeNodeDoubleClick(_: MouseEvent, node: Node) {
    setSelectedNode(node);
  }

  const onAgentConnect = useCallback((conn: Connection) => {
    setAgentEdges(prev => addEdge(conn, prev));
    setDirty(true);
  }, [setAgentEdges]);

  // ── Context menu ──────────────────────────────────────────────────────────────
  type CtxTarget =
    | { kind: 'node'; node: Node }
    | { kind: 'edge'; edge: Edge };

  const [ctxMenu, setCtxMenu] = useState<{ x: number; y: number; target: CtxTarget } | null>(null);

  const closeCtx = useCallback(() => setCtxMenu(null), []);

  const onNodeCtx = useCallback((e: MouseEvent, node: Node) => {
    e.preventDefault();
    setCtxMenu({ x: e.clientX, y: e.clientY, target: { kind: 'node', node } });
    setSelectedNode(node);
  }, []);

  const onEdgeCtx = useCallback((e: MouseEvent, edge: Edge) => {
    e.preventDefault();
    setCtxMenu({ x: e.clientX, y: e.clientY, target: { kind: 'edge', edge } });
  }, []);

  const ctxDelete = useCallback(() => {
    if (!ctxMenu) return;
    if (ctxMenu.target.kind === 'node') {
      const id = ctxMenu.target.node.id;
      if (activeView === 'agent') {
        setAgentNodes(prev => prev.filter(n => n.id !== id));
        setAgentEdges(prev => prev.filter(e => e.source !== id && e.target !== id));
      } else {
        setLocalPipeNodes(prev => prev.filter(n => n.id !== id));
        setLocalPipeEdges(prev => prev.filter(e => e.source !== id && e.target !== id));
      }
    } else {
      const id = ctxMenu.target.edge.id;
      if (activeView === 'agent') {
        setAgentEdges(prev => prev.filter(e => e.id !== id));
      } else {
        setLocalPipeEdges(prev => prev.filter(e => e.id !== id));
      }
    }
    setDirty(true);
    closeCtx();
  }, [ctxMenu, activeView, setAgentNodes, setAgentEdges, setLocalPipeNodes, setLocalPipeEdges, closeCtx]);

  const ctxEditPipeline = useCallback(() => {
    if (!ctxMenu || ctxMenu.target.kind !== 'node') return;
    const node = ctxMenu.target.node;
    if (node.type === 'skill') {
      savePipelineState();
      const sd = node.data as unknown as SkillData;
      setActiveSkillId(sd.skill_id);
      setActiveView('skill');
      setSelectedNode(null);
    }
    closeCtx();
  }, [ctxMenu, savePipelineState, closeCtx]);

  const onPipeConnect = useCallback((conn: Connection) => {
    // Remove any existing incoming edge to the target before adding the new one.
    // This allows re-routing (e.g. Input→Response replaced by LLM→Response).
    setLocalPipeEdges(prev => addEdge(conn, prev.filter(e => e.target !== conn.target)));
    setDirty(true);

    // Auto-fill: read source output_var → suggest it in target input field (only if empty).
    setLocalPipeNodes(prev => {
      const sourceNode = prev.find(n => n.id === conn.source);
      const targetNode = prev.find(n => n.id === conn.target);
      if (!sourceNode || !targetNode) return prev;

      const srcData = sourceNode.data as unknown as StepData;
      const tgtData = targetNode.data as unknown as StepData;

      // Resolve what variable name the source produces.
      const sourceVar: string =
        srcData.step_type === 'input'
          ? ((srcData.config?.bindings as Record<string, string>)?.text || 'input')
          : ((srcData.config?.output_var as string) || 'output');

      // Find which field on the target to auto-fill.
      const targetField = STEP_INPUT_FIELD[tgtData.step_type];
      if (!targetField) return prev;

      // from_var always updates (it should reflect whatever is now connected upstream).
      // Other fields only fill if currently empty — don't overwrite user-typed content.
      const currentValue = tgtData.config?.[targetField] as string | undefined;
      if (targetField !== 'from_var' && currentValue && currentValue.trim() !== '') return prev;

      const fillValue = targetField === 'from_var' ? sourceVar : `{{${sourceVar}}}`;

      return prev.map(n =>
        n.id === conn.target
          ? { ...n, data: { ...n.data, config: { ...(n.data.config as Record<string, unknown>), [targetField]: fillValue } } }
          : n
      );
    });
  }, [setLocalPipeEdges, setLocalPipeNodes]);

  const isPipeConnectionValid = useCallback((_conn: Connection) => {
    return true;
  }, []);

  // Properties panel update
  function updateSelectedNodeField(field: string, value: string) {
    if (!selectedNode) return;
    if (activeView === 'agent') {
      setAgentNodes(prev => prev.map(n =>
        n.id === selectedNode.id ? { ...n, data: { ...n.data, [field]: value } } : n
      ));
      // Auto-populate slug from display_name when slug hasn't been manually set
      if (field === 'display_name' && selectedNode.id === 'agent-root' && !defId) {
        const slug = value.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '');
        setAgentSlug(slug);
      }
    } else {
      setLocalPipeNodes(prev => prev.map(n =>
        n.id === selectedNode.id ? { ...n, data: { ...n.data, [field]: value } } : n
      ));
    }
    setDirty(true);
  }

  // Update a single key inside a step node's config object.
  function updateStepConfig(key: string, value: unknown) {
    if (!selectedNode || activeView !== 'skill') return;
    setLocalPipeNodes(prev => prev.map(n =>
      n.id === selectedNode.id
        ? { ...n, data: { ...n.data, config: { ...(n.data.config as Record<string, unknown>), [key]: value } } }
        : n
    ));
    setDirty(true);
  }

  const currentNodes = activeView === 'agent' ? agentNodes : localPipeNodes;
  const currentEdges = activeView === 'agent' ? agentEdges : localPipeEdges;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100vh', background: C.bg }}>
      {/* Toolbar */}
      <div style={{
        display: 'flex', alignItems: 'center', gap: '12px',
        padding: '12px 24px', borderBottom: `1px solid ${C.outline}`,
        background: C.surface, flexShrink: 0,
      }}>
        <button onClick={handleBack} style={{
          background: 'transparent', border: `1px solid ${C.outline}`, color: C.textMuted,
          padding: '6px 14px', borderRadius: '6px', cursor: 'pointer', fontSize: '13px',
        }}>
          {activeView === 'skill' ? 'Back to Agent' : 'Back to Agents'}
        </button>

        <div style={{ flex: 1 }}>
          {activeView === 'agent' ? (
            <div style={{ display: 'flex', alignItems: 'center', gap: '12px' }}>
              <input
                value={agentSlug}
                onChange={e => { setAgentSlug(e.target.value); setDirty(true); }}
                placeholder="agent-slug (kebab-case)"
                style={{
                  background: 'transparent', border: `1px solid ${C.outline}`, color: '#fff',
                  padding: '6px 12px', borderRadius: '6px', fontSize: '13px', width: '220px',
                }}
              />
              <span style={{ color: C.textMuted, fontSize: '12px' }}>Agent Builder</span>
            </div>
          ) : (
            <span style={{ color: C.purple, fontWeight: 600, fontSize: '14px' }}>
              Pipeline: {activeSkillId}
            </span>
          )}
        </div>

        {(saveError || publishError) && (
          <span style={{ color: '#f87171', fontSize: '12px', maxWidth: '300px' }}>{saveError || publishError}</span>
        )}
        {publishedRevision !== null && (
          <span style={{ color: '#34d399', fontSize: '12px' }}>Published rev {publishedRevision}</span>
        )}

        {defId && (
          <button onClick={handleDelete} disabled={deleting} style={{
            background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.4)',
            color: '#f87171', padding: '6px 14px', borderRadius: '6px', cursor: 'pointer', fontSize: '13px',
          }}>
            {deleting ? 'Deleting...' : 'Delete Draft'}
          </button>
        )}
        {defId && (
          <button onClick={handleValidate} disabled={validating} style={{
            background: 'rgba(52,211,153,0.1)', border: '1px solid rgba(52,211,153,0.4)',
            color: '#34d399', padding: '6px 14px', borderRadius: '6px', cursor: 'pointer', fontSize: '13px',
          }}>
            {validating ? 'Validating...' : 'Validate'}
          </button>
        )}
        {defId && (
          <button onClick={handlePublish} disabled={publishing} style={{
            background: publishing ? 'rgba(0,240,255,0.1)' : 'rgba(0,240,255,0.15)',
            border: '1px solid rgba(0,240,255,0.4)',
            color: '#00f0ff', padding: '6px 14px', borderRadius: '6px', cursor: 'pointer', fontSize: '13px',
          }}>
            {publishing ? 'Publishing...' : 'Publish'}
          </button>
        )}
        <button onClick={handleSave} disabled={saving} style={{
          background: dirty ? C.cyan : 'rgba(0,240,255,0.2)',
          border: 'none', color: '#000', fontWeight: 700,
          padding: '7px 20px', borderRadius: '6px', cursor: 'pointer', fontSize: '13px',
          opacity: saving ? 0.7 : 1,
        }}>
          {saving ? 'Saving...' : defId ? 'Save Changes' : 'Create Draft'}
        </button>
      </div>

      {loadError && (
        <div style={{ background: 'rgba(239,68,68,0.1)', padding: '10px 24px', color: '#f87171', fontSize: '13px' }}>
          {loadError}
        </div>
      )}

      <div style={{ display: 'flex', flex: 1, overflow: 'hidden' }}>
        {/* ── Node Library (left panel) ── */}
        <div style={{
          width: '220px', flexShrink: 0, borderRight: `1px solid ${C.outline}`,
          background: C.surface, overflowY: 'auto', display: 'flex', flexDirection: 'column',
        }}>
          <div style={{ padding: '14px 14px 8px', fontSize: 10, fontWeight: 700, color: C.textMuted, letterSpacing: 1.5, textTransform: 'uppercase', borderBottom: `1px solid ${C.outline}` }}>
            {activeView === 'agent' ? 'Node Library' : 'Step Library'}
          </div>

          <div style={{ padding: '12px 10px', display: 'flex', flexDirection: 'column', gap: 6, flex: 1 }}>
            {activeView === 'agent' ? (
              <>
                {/* Skill draggable card */}
                <div style={{ fontSize: 10, fontWeight: 700, color: C.textMuted, letterSpacing: 1, textTransform: 'uppercase', marginBottom: 4 }}>Skills</div>
                <div
                  draggable
                  onDragStart={e => { e.dataTransfer.setData('nodeType', 'skill'); e.dataTransfer.effectAllowed = 'move'; }}
                  style={{
                    display: 'flex', alignItems: 'center', gap: 10, padding: '9px 12px',
                    borderRadius: 8, cursor: 'grab', userSelect: 'none',
                    background: C.purpleBg, border: `1px solid ${C.purpleBorder}`,
                  }}
                >
                  <span style={{ fontSize: 18 }}>⚡</span>
                  <div>
                    <div style={{ fontSize: 13, fontWeight: 600, color: C.purple }}>Skill</div>
                    <div style={{ fontSize: 10, color: C.textMuted }}>Named capability</div>
                  </div>
                </div>

                {/* Credential slots section */}
                <div style={{ fontSize: 10, fontWeight: 700, color: C.textMuted, letterSpacing: 1, textTransform: 'uppercase', marginTop: 12, marginBottom: 4 }}>Credential Slots</div>
                <button onClick={() => {
                  setCredentialSlots(prev => [...prev, { name: `slot_${prev.length + 1}`, description: '', required: true }]);
                  setDirty(true);
                }} style={{
                  display: 'flex', alignItems: 'center', gap: 8, padding: '9px 12px',
                  borderRadius: 8, cursor: 'pointer', background: C.amberBg,
                  border: `1px solid ${C.amberBorder}`, color: C.amber,
                  fontSize: 13, fontWeight: 600, width: '100%', textAlign: 'left',
                }}>
                  <span style={{ fontSize: 16 }}>🔑</span> + Add Slot
                </button>
                {credentialSlots.map((slot, i) => (
                  <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 4, background: C.amberBg, border: `1px solid ${C.amberBorder}`, borderRadius: 6, padding: '4px 8px' }}>
                    <span style={{ fontSize: 12, color: C.amber, flexShrink: 0 }}>🔑</span>
                    <input
                      value={slot.name}
                      onChange={e => {
                        const next = [...credentialSlots];
                        next[i] = { ...next[i], name: e.target.value };
                        setCredentialSlots(next);
                        setDirty(true);
                      }}
                      style={{ flex: 1, background: 'transparent', border: 'none', color: C.amber, fontSize: '11px', outline: 'none', minWidth: 0 }}
                      placeholder="slot name"
                    />
                    <button onClick={() => { setCredentialSlots(prev => prev.filter((_, j) => j !== i)); setDirty(true); }}
                      style={{ background: 'transparent', border: 'none', color: '#f87171', cursor: 'pointer', fontSize: 14, padding: '0 2px', flexShrink: 0 }}>×</button>
                  </div>
                ))}
              </>
            ) : (
              <>
                {/* Step type cards — grouped */}
                {[
                  { label: 'Data Flow', items: [
                    { type: 'input',    desc: 'Bind caller input' },
                    { type: 'response', desc: 'Return result' },
                  ]},
                  { label: 'Processing', items: [
                    { type: 'llm',       desc: 'Call an LLM' },
                    { type: 'transform', desc: 'Template expressions' },
                    { type: 'http',      desc: 'HTTP tool call' },
                  ]},
                  { label: 'Advanced', items: [
                    { type: 'branch',     desc: 'Conditional branch' },
                    { type: 'loop',       desc: 'Repeat steps' },
                    { type: 'parallel',   desc: 'Run in parallel' },
                    { type: 'a2a_call',   desc: 'Call another agent' },
                    { type: 'human_wait', desc: 'Wait for human' },
                    { type: 'stream_out', desc: 'Stream output' },
                  ]},
                ].map(group => (
                  <div key={group.label}>
                    <div style={{ fontSize: 10, fontWeight: 700, color: C.textMuted, letterSpacing: 1, textTransform: 'uppercase', margin: '8px 0 4px' }}>{group.label}</div>
                    {group.items.map(st => {
                      const meta = STEP_META[st.type] ?? { border: C.indigoBorder, emoji: '🔧', label: st.type };
                      return (
                        <div
                          key={st.type}
                          draggable
                          onDragStart={e => { e.dataTransfer.setData('nodeType', 'step'); e.dataTransfer.setData('stepType', st.type); e.dataTransfer.effectAllowed = 'move'; }}
                          onClick={() => addStepToActivePipeline(st.type as AgentStepDoc['type'])}
                          style={{
                            display: 'flex', alignItems: 'center', gap: 8, padding: '7px 10px',
                            borderRadius: 7, cursor: 'grab', userSelect: 'none', marginBottom: 3,
                            background: `${meta.border}18`, border: `1px solid ${meta.border}`,
                          }}
                        >
                          <span style={{ fontSize: 18, width: 22, textAlign: 'center', flexShrink: 0 }}>{meta.emoji}</span>
                          <div style={{ minWidth: 0 }}>
                            <div style={{ fontSize: 12, fontWeight: 600, color: meta.border }}>{meta.label}</div>
                            <div style={{ fontSize: 10, color: C.textMuted }}>{st.desc}</div>
                          </div>
                        </div>
                      );
                    })}
                  </div>
                ))}
              </>
            )}
          </div>
        </div>

        {/* Canvas */}
        <div style={{ flex: 1, position: 'relative' }}>
          {/* Context menu */}
          {ctxMenu && (
            <div
              onMouseLeave={closeCtx}
              style={{
                position: 'fixed', zIndex: 9999,
                left: ctxMenu.x, top: ctxMenu.y,
                background: '#1e293b', border: '1px solid #334155',
                borderRadius: '8px', padding: '4px', minWidth: '160px',
                boxShadow: '0 8px 24px rgba(0,0,0,0.5)',
              }}
            >
              {ctxMenu.target.kind === 'node' && (
                <>
                  <div style={{ padding: '4px 8px', fontSize: '10px', color: '#64748b', fontWeight: 700, textTransform: 'uppercase', letterSpacing: '0.08em' }}>
                    {ctxMenu.target.node.type === 'agentRoot' ? 'Agent' : ctxMenu.target.node.type === 'skill' ? 'Skill' : (ctxMenu.target.node.data as unknown as StepData).step_type}
                  </div>
                  <button onClick={() => { setSelectedNode(ctxMenu.target.kind === 'node' ? ctxMenu.target.node : null); closeCtx(); }} style={ctxItemStyle}>
                    ✏️ Properties
                  </button>
                  {ctxMenu.target.node.type === 'skill' && (
                    <button onClick={ctxEditPipeline} style={ctxItemStyle}>
                      ⚡ Edit Pipeline
                    </button>
                  )}
                  <div style={{ borderTop: '1px solid #334155', margin: '4px 0' }} />
                </>
              )}
              <button onClick={ctxDelete} style={{ ...ctxItemStyle, color: '#f87171' }}>
                🗑️ Delete
              </button>
            </div>
          )}

          {activeView === 'agent' ? (
            <ReactFlow
              nodes={currentNodes}
              edges={currentEdges}
              onNodesChange={onAgentNodesChange}
              onEdgesChange={onAgentEdgesChange}
              onConnect={onAgentConnect}
              onNodeContextMenu={onNodeCtx}
              onEdgeContextMenu={onEdgeCtx}
              onNodeClick={(_: MouseEvent, node: Node) => { setSelectedNode(node); closeCtx(); }}
              onNodeDoubleClick={onAgentNodeDoubleClick}
              onPaneClick={() => { setSelectedNode(null); closeCtx(); }}
              nodeTypes={nodeTypes}
              onDragOver={(e: DragEvent) => { e.preventDefault(); e.dataTransfer.dropEffect = 'move'; }}
              onDrop={(e: DragEvent) => {
                e.preventDefault();
                const nodeType = e.dataTransfer.getData('nodeType');
                if (nodeType === 'skill') {
                  const bounds = (e.currentTarget as HTMLElement).getBoundingClientRect();
                  const pos = screenToFlowPosition({ x: e.clientX - bounds.left, y: e.clientY - bounds.top });
                  const sid = genUUID();
                  const newNode: Node = { id: `skill-${sid}`, type: 'skill', position: pos, data: { skill_id: sid, name: 'New Skill', description: '', tags: [], input_modes: ['text/plain'], output_modes: ['text/plain'], examples: [] } };
                  setAgentNodes(prev => [...prev, newNode]);
                  setAgentEdges(prev => [...prev, { id: `root-to-${sid}`, source: 'agent-root', target: `skill-${sid}` }]);
                  setSkillPipelines(prev => ({ ...prev, [sid]: makeDefaultPipeline() }));
                  setDirty(true);
                }
              }}
              fitView
            >
              <Background variant={BackgroundVariant.Dots} gap={20} color="rgba(255,255,255,0.05)" />
              <Controls />
            </ReactFlow>
          ) : (
            <ReactFlow
              nodes={localPipeNodes}
              edges={localPipeEdges}
              onNodesChange={onPipeNodesChange}
              onEdgesChange={onPipeEdgesChange}
              onConnect={onPipeConnect}
              isValidConnection={isPipeConnectionValid}
              onNodeContextMenu={onNodeCtx}
              onEdgeContextMenu={onEdgeCtx}
              onNodeClick={(_: MouseEvent, node: Node) => { setSelectedNode(node); closeCtx(); }}
              onNodeDoubleClick={onPipeNodeDoubleClick}
              onPaneClick={() => { setSelectedNode(null); closeCtx(); }}
              nodeTypes={nodeTypes}
              onDragOver={(e: DragEvent) => { e.preventDefault(); e.dataTransfer.dropEffect = 'move'; }}
              onDrop={(e: DragEvent) => {
                e.preventDefault();
                const nodeType = e.dataTransfer.getData('nodeType');
                if (nodeType === 'step') {
                  const stepType = e.dataTransfer.getData('stepType') as AgentStepDoc['type'];
                  const bounds = (e.currentTarget as HTMLElement).getBoundingClientRect();
                  const pos = screenToFlowPosition({ x: e.clientX - bounds.left, y: e.clientY - bounds.top });
                  const stepId = genUUID();
                  const newNode: Node = { id: `step-${stepId}`, type: 'step', position: pos, data: { step_id: stepId, step_type: stepType, label: stepType.replace('_', ' '), config: {} } };
                  setLocalPipeNodes(prev => [...prev, newNode]);
                  setDirty(true);
                }
              }}
              fitView
            >
              <Background variant={BackgroundVariant.Dots} gap={20} color="rgba(255,255,255,0.05)" />
              <Controls />
            </ReactFlow>
          )}
        </div>

        {/* Properties panel */}
        {selectedNode && (
          <div style={{
            width: '300px', flexShrink: 0, borderLeft: `1px solid ${C.outline}`,
            background: C.surface, padding: '16px', overflowY: 'auto',
          }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '16px' }}>
              <span style={{ color: C.text, fontWeight: 700, fontSize: '13px' }}>Properties</span>
              <button onClick={() => setSelectedNode(null)} style={{ background: 'transparent', border: 'none', color: C.textMuted, cursor: 'pointer', fontSize: '16px' }}>x</button>
            </div>

            {selectedNode.type === 'agentRoot' && (() => {
              const d = (agentNodes.find(n => n.id === selectedNode.id)?.data ?? selectedNode.data) as unknown as AgentRootData;
              return (
                <>
                  <label style={{ color: C.textMuted, fontSize: '11px', fontWeight: 700, display: 'block', marginBottom: '4px' }}>Display Name</label>
                  <input
                    value={d.display_name}
                    onChange={e => updateSelectedNodeField('display_name', e.target.value)}
                    style={{ width: '100%', background: 'transparent', border: `1px solid ${C.outline}`, color: '#fff', padding: '6px', borderRadius: '4px', fontSize: '13px', boxSizing: 'border-box' }}
                  />
                  <label style={{ color: C.textMuted, fontSize: '11px', fontWeight: 700, display: 'block', marginTop: '12px', marginBottom: '4px' }}>Description</label>
                  <textarea
                    value={d.description}
                    onChange={e => updateSelectedNodeField('description', e.target.value)}
                    rows={3}
                    style={{ width: '100%', background: 'transparent', border: `1px solid ${C.outline}`, color: '#fff', padding: '6px', borderRadius: '4px', fontSize: '13px', resize: 'vertical', boxSizing: 'border-box' }}
                  />
                  <label style={{ color: C.textMuted, fontSize: '11px', fontWeight: 700, display: 'block', marginTop: '12px', marginBottom: '4px' }}>Version</label>
                  <input
                    value={d.version}
                    onChange={e => updateSelectedNodeField('version', e.target.value)}
                    style={{ width: '100%', background: 'transparent', border: `1px solid ${C.outline}`, color: '#fff', padding: '6px', borderRadius: '4px', fontSize: '13px', boxSizing: 'border-box' }}
                  />
                </>
              );
            })()}

            {selectedNode.type === 'skill' && (() => {
              const liveSkillNode = agentNodes.find(n => n.id === selectedNode.id);
              const d = (liveSkillNode?.data ?? selectedNode.data) as unknown as SkillData;
              const skillNodeId = selectedNode.id;
              const MODES = ['text/plain', 'text/markdown', 'application/json', 'application/octet-stream'];
              function updateSkillArray(field: keyof SkillData, arr: string[]) {
                if (activeView === 'agent') {
                  setAgentNodes(prev => prev.map(n =>
                    n.id === skillNodeId ? { ...n, data: { ...n.data, [field]: arr } } : n
                  ));
                }
                setDirty(true);
              }
              function toggleMode(field: 'input_modes' | 'output_modes', mode: string) {
                const current = (d[field] ?? []) as string[];
                const next = current.includes(mode) ? current.filter(m => m !== mode) : [...current, mode];
                updateSkillArray(field, next.length ? next : [mode]);
              }
              return (
                <>
                  <label style={labelStyle}>Skill ID <span style={{ fontWeight: 400, color: '#475569' }}>(auto-generated)</span></label>
                  <div style={{ ...inputStyle, fontFamily: 'JetBrains Mono, monospace', fontSize: '10px', color: '#475569', userSelect: 'all', cursor: 'text', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {d.skill_id}
                  </div>
                  <div style={fieldGap}>
                    <label style={labelStyle}>Name</label>
                    <input
                      value={d.name}
                      onChange={e => updateSelectedNodeField('name', e.target.value)}
                      style={inputStyle}
                    />
                  </div>
                  <div style={fieldGap}>
                    <label style={labelStyle}>Description</label>
                    <textarea
                      value={d.description}
                      onChange={e => updateSelectedNodeField('description', e.target.value)}
                      rows={2}
                      style={textareaStyle}
                    />
                  </div>

                  <div style={fieldGap}>
                    <label style={labelStyle}>Input Modes</label>
                    {MODES.map(m => (
                      <label key={m} style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4, cursor: 'pointer', fontSize: '12px', color: '#ccc' }}>
                        <input
                          type="checkbox"
                          checked={(d.input_modes ?? []).includes(m)}
                          onChange={() => toggleMode('input_modes', m)}
                          style={{ accentColor: C.cyan }}
                        />
                        {m}
                      </label>
                    ))}
                  </div>

                  <div style={fieldGap}>
                    <label style={labelStyle}>Output Modes</label>
                    {MODES.map(m => (
                      <label key={m} style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4, cursor: 'pointer', fontSize: '12px', color: '#ccc' }}>
                        <input
                          type="checkbox"
                          checked={(d.output_modes ?? []).includes(m)}
                          onChange={() => toggleMode('output_modes', m)}
                          style={{ accentColor: C.purple }}
                        />
                        {m}
                      </label>
                    ))}
                  </div>

                  <div style={fieldGap}>
                    <label style={labelStyle}>Tags <span style={hint}>comma-separated</span></label>
                    <input
                      value={(d.tags ?? []).join(', ')}
                      onChange={e => updateSkillArray('tags', e.target.value.split(',').map(t => t.trim()).filter(Boolean))}
                      style={inputStyle}
                      placeholder="search, nlp, ..."
                    />
                  </div>

                  <div style={fieldGap}>
                    <label style={labelStyle}>Examples</label>
                    {(d.examples ?? []).map((ex, i) => (
                      <div key={i} style={{ display: 'flex', gap: 4, marginBottom: 4 }}>
                        <input
                          value={ex}
                          onChange={e => {
                            const next = [...(d.examples ?? [])];
                            next[i] = e.target.value;
                            updateSkillArray('examples', next);
                          }}
                          style={{ ...inputStyle, flex: 1, fontSize: '12px' }}
                          placeholder="e.g. Summarize this article"
                        />
                        <button
                          onClick={() => updateSkillArray('examples', (d.examples ?? []).filter((_, j) => j !== i))}
                          style={{ background: 'transparent', border: 'none', color: '#f87171', cursor: 'pointer', fontSize: '14px', padding: '0 4px' }}
                        >×</button>
                      </div>
                    ))}
                    <button
                      onClick={() => updateSkillArray('examples', [...(d.examples ?? []), ''])}
                      style={{ marginTop: 4, background: 'transparent', border: `1px dashed ${C.outline}`, color: C.textMuted, padding: '4px 10px', borderRadius: '4px', cursor: 'pointer', fontSize: '11px', width: '100%' }}
                    >+ Add example</button>
                  </div>

                  <button onClick={() => {
                    savePipelineState();
                    setActiveSkillId(d.skill_id);
                    setActiveView('skill');
                    setSelectedNode(null);
                  }} style={{
                    marginTop: '16px', width: '100%', background: C.purpleBg,
                    border: `1px solid ${C.purpleBorder}`, color: C.purple,
                    padding: '8px', borderRadius: '6px', cursor: 'pointer', fontSize: '12px', fontWeight: 600,
                  }}>
                    Edit Pipeline
                  </button>
                </>
              );
            })()}

            {selectedNode.type === 'step' && (() => {
              const d = (localPipeNodes.find(n => n.id === selectedNode.id)?.data ?? selectedNode.data) as unknown as StepData;
              const cfg = d.config ?? {};

              // Helper: get config value with fallback.
              function cfgStr(key: string): string { return (cfg[key] as string) ?? ''; }
              function cfgNum(key: string, def = 0): number { return (cfg[key] as number) ?? def; }

              return (
                <>
                  {/* ── Identity (always shown) ── */}
                  <div style={{ marginBottom: '2px' }}>
                    <label style={labelStyle}>Label</label>
                    <input
                      value={d.label}
                      onChange={e => updateSelectedNodeField('label', e.target.value)}
                      style={inputStyle}
                    />
                  </div>
                  <div style={fieldGap}>
                    <label style={labelStyle}>Step ID <span style={{ fontWeight: 400, color: '#475569' }}>(auto-generated)</span></label>
                    <div style={{ ...inputStyle, fontFamily: 'JetBrains Mono, monospace', fontSize: '10px', color: '#475569', userSelect: 'all', cursor: 'text', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {d.step_id}
                    </div>
                  </div>
                  <div style={{ ...fieldGap, marginBottom: '16px' }}>
                    <label style={labelStyle}>Type</label>
                    <div style={{ ...inputStyle, color: C.textMuted, cursor: 'default' }}>{d.step_type}</div>
                  </div>

                  {/* ── INPUTS: live incoming connections ── */}
                  {d.step_type !== 'input' && (() => {
                    const inEdges = localPipeEdges.filter(e => e.target === selectedNode.id);
                    return (
                      <div style={{ marginBottom: '16px' }}>
                        <div style={{ fontSize: '10px', fontWeight: 700, letterSpacing: '0.08em', color: C.cyan, marginBottom: '6px' }}>
                          INPUTS {inEdges.length === 0 && <span style={{ color: C.textMuted, fontWeight: 400 }}>— nothing connected</span>}
                        </div>
                        {inEdges.map(e => {
                          const src = localPipeNodes.find(n => n.id === e.source);
                          const srcData = src?.data as unknown as StepData | undefined;
                          const srcMeta = srcData ? (STEP_META[srcData.step_type] ?? { emoji: '🔧', label: srcData.step_type }) : { emoji: '?', label: 'unknown' };
                          const srcVar = srcData?.step_type === 'input'
                            ? ((srcData.config?.bindings as Record<string,string>)?.text || 'input')
                            : ((srcData?.config?.output_var as string) || 'output');
                          return (
                            <div key={e.id} style={{ display: 'flex', alignItems: 'center', gap: '8px', padding: '6px 8px', borderRadius: '6px', marginBottom: '4px', background: 'rgba(0,240,255,0.05)', border: '1px solid rgba(0,240,255,0.15)' }}>
                              <span style={{ fontSize: '14px' }}>{srcMeta.emoji}</span>
                              <span style={{ color: '#e2e8f0', fontSize: '11px', fontWeight: 600 }}>{srcData?.label || srcMeta.label}</span>
                              <span style={{ color: C.textMuted, fontSize: '11px' }}>→</span>
                              <code style={{ color: C.cyan, fontSize: '11px', fontFamily: 'monospace' }}>{`{{${srcVar}}}`}</code>
                            </div>
                          );
                        })}
                      </div>
                    );
                  })()}

                  {/* ── Config: input ── */}
                  {d.step_type === 'input' && (
                    <>
                      <div style={{ color: C.cyan, fontSize: '10px', fontWeight: 700, letterSpacing: '0.08em', marginBottom: '10px' }}>INPUT CONFIG</div>
                      <label style={labelStyle}>Bind text input to variable</label>
                      <input
                        value={cfgStr('text_var') || ((cfg.bindings as Record<string,string>)?.text ?? '')}
                        onChange={e => updateStepConfig('bindings', { text: e.target.value })}
                        style={inputStyle}
                        placeholder="e.g. user_query"
                      />
                      <div style={{ marginTop: 6, fontSize: 11, color: '#64748b' }}>
                        The caller's message text will be available as <code style={{ color: C.cyan }}>{'{{.' + (cfgStr('text_var') || ((cfg.bindings as Record<string,string>)?.text) || 'user_query') + '}}'}</code> in downstream steps.
                      </div>
                    </>
                  )}

                  {/* ── Config: llm ── */}
                  {d.step_type === 'llm' && (
                    <>
                      <div style={{ color: C.purple, fontSize: '10px', fontWeight: 700, letterSpacing: '0.08em', marginBottom: '10px' }}>LLM CONFIG</div>

                      <label style={labelStyle}>Model</label>
                      <select
                        value={cfgStr('model') || 'claude-haiku-4-5-20251001'}
                        onChange={e => updateStepConfig('model', e.target.value)}
                        style={selectStyle}
                      >
                        {LLM_MODELS.anthropic.map(m => (
                          <option key={m} value={m}>{m}</option>
                        ))}
                      </select>

                      <div style={fieldGap}>
                        <label style={labelStyle}>Max Tokens</label>
                        <input
                          type="number" min={1} max={32000}
                          value={cfgNum('max_tokens', 4096)}
                          onChange={e => updateStepConfig('max_tokens', parseInt(e.target.value) || 4096)}
                          style={inputStyle}
                        />
                      </div>

                      <div style={fieldGap}>
                        <label style={labelStyle}>System Prompt</label>
                        <textarea
                          rows={4}
                          value={cfgStr('system_prompt')}
                          onChange={e => updateStepConfig('system_prompt', e.target.value)}
                          style={textareaStyle}
                          placeholder="You are a helpful assistant..."
                        />
                      </div>

                      <div style={fieldGap}>
                        <label style={labelStyle}>
                          User Prompt <span style={hint}>Go template · leave blank to pass caller input directly</span>
                        </label>
                        <textarea
                          rows={3}
                          value={cfgStr('user_prompt')}
                          onChange={e => updateStepConfig('user_prompt', e.target.value)}
                          style={textareaStyle}
                          placeholder={'{{.user_query}}'}
                        />
                      </div>

                      <div style={fieldGap}>
                        <label style={labelStyle}>Output Variable <span style={hint}>default: output</span></label>
                        <input
                          value={cfgStr('output_var')}
                          onChange={e => updateStepConfig('output_var', e.target.value)}
                          style={inputStyle}
                          placeholder="output"
                        />
                      </div>

                      <div style={fieldGap}>
                        <label style={labelStyle}>API Key Slot <span style={hint}>blank = platform key</span></label>
                        <select
                          value={cfgStr('provider_key_slot')}
                          onChange={e => updateStepConfig('provider_key_slot', e.target.value)}
                          style={selectStyle}
                        >
                          <option value="">— platform key —</option>
                          {credentialSlots.map(s => (
                            <option key={s.name} value={s.name}>{s.name}</option>
                          ))}
                        </select>
                      </div>
                    </>
                  )}

                  {/* ── Config: http ── */}
                  {d.step_type === 'http' && (
                    <>
                      <div style={{ color: C.amber, fontSize: '10px', fontWeight: 700, letterSpacing: '0.08em', marginBottom: '10px' }}>HTTP CONFIG</div>

                      <label style={labelStyle}>Method</label>
                      <select
                        value={cfgStr('method') || 'GET'}
                        onChange={e => updateStepConfig('method', e.target.value)}
                        style={selectStyle}
                      >
                        {['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].map(m => (
                          <option key={m} value={m}>{m}</option>
                        ))}
                      </select>

                      <div style={fieldGap}>
                        <label style={labelStyle}>URL <span style={hint}>Go template</span></label>
                        <input
                          value={cfgStr('url_template')}
                          onChange={e => updateStepConfig('url_template', e.target.value)}
                          style={inputStyle}
                          placeholder="https://api.example.com/{{.resource}}"
                        />
                      </div>

                      <div style={fieldGap}>
                        <label style={labelStyle}>Body Template <span style={hint}>Go template · optional</span></label>
                        <textarea
                          rows={3}
                          value={cfgStr('body_template')}
                          onChange={e => updateStepConfig('body_template', e.target.value)}
                          style={textareaStyle}
                          placeholder={'{"query": "{{.user_query}}"}'}
                        />
                      </div>

                      <div style={fieldGap}>
                        <label style={labelStyle}>Timeout (seconds)</label>
                        <input
                          type="number" min={1} max={300}
                          value={cfgNum('timeout_seconds', 30)}
                          onChange={e => updateStepConfig('timeout_seconds', parseInt(e.target.value) || 30)}
                          style={inputStyle}
                        />
                      </div>

                      <div style={fieldGap}>
                        <label style={labelStyle}>Credential Slot <span style={hint}>blank = no auth</span></label>
                        <select
                          value={cfgStr('credential_slot')}
                          onChange={e => updateStepConfig('credential_slot', e.target.value)}
                          style={selectStyle}
                        >
                          <option value="">— none —</option>
                          {credentialSlots.map(s => (
                            <option key={s.name} value={s.name}>{s.name}</option>
                          ))}
                        </select>
                      </div>

                      {cfgStr('credential_slot') && (
                        <>
                          <div style={fieldGap}>
                            <label style={labelStyle}>Inject Mode</label>
                            <select
                              value={(cfg.credential_inject as {mode?: string})?.mode ?? 'header'}
                              onChange={e => updateStepConfig('credential_inject', {
                                ...(cfg.credential_inject as object ?? {}),
                                mode: e.target.value,
                              })}
                              style={selectStyle}
                            >
                              <option value="header">Header</option>
                              <option value="query">Query param</option>
                              <option value="basic">HTTP Basic</option>
                            </select>
                          </div>

                          {((cfg.credential_inject as {mode?: string})?.mode ?? 'header') === 'header' && (
                            <>
                              <div style={fieldGap}>
                                <label style={labelStyle}>Header Name</label>
                                <input
                                  value={(cfg.credential_inject as {header_name?: string})?.header_name ?? 'Authorization'}
                                  onChange={e => updateStepConfig('credential_inject', {
                                    ...(cfg.credential_inject as object ?? {}),
                                    header_name: e.target.value,
                                  })}
                                  style={inputStyle}
                                  placeholder="Authorization"
                                />
                              </div>
                              <div style={fieldGap}>
                                <label style={labelStyle}>Value Template</label>
                                <input
                                  value={(cfg.credential_inject as {value_template?: string})?.value_template ?? 'Bearer {credential}'}
                                  onChange={e => updateStepConfig('credential_inject', {
                                    ...(cfg.credential_inject as object ?? {}),
                                    value_template: e.target.value,
                                  })}
                                  style={inputStyle}
                                  placeholder="Bearer {credential}"
                                />
                              </div>
                            </>
                          )}

                          {((cfg.credential_inject as {mode?: string})?.mode) === 'query' && (
                            <div style={fieldGap}>
                              <label style={labelStyle}>Query Param Name</label>
                              <input
                                value={(cfg.credential_inject as {query_param?: string})?.query_param ?? ''}
                                onChange={e => updateStepConfig('credential_inject', {
                                  ...(cfg.credential_inject as object ?? {}),
                                  query_param: e.target.value,
                                })}
                                style={inputStyle}
                                placeholder="api_key"
                              />
                            </div>
                          )}
                        </>
                      )}

                      {/* Response extractions */}
                      <div style={{ ...fieldGap, marginTop: '16px' }}>
                        <label style={labelStyle}>Response Extractions <span style={hint}>JSONPath → variable</span></label>
                        {((cfg.extractions as {var: string; json_path: string}[]) ?? []).map((ex, i) => (
                          <div key={i} style={{ display: 'flex', gap: 4, marginBottom: 4 }}>
                            <input
                              value={ex.json_path}
                              onChange={e => {
                                const next = [...((cfg.extractions as {var: string; json_path: string}[]) ?? [])];
                                next[i] = { ...next[i], json_path: e.target.value };
                                updateStepConfig('extractions', next);
                              }}
                              style={{ ...inputStyle, flex: 1, fontSize: '11px' }}
                              placeholder="$.result"
                            />
                            <input
                              value={ex.var}
                              onChange={e => {
                                const next = [...((cfg.extractions as {var: string; json_path: string}[]) ?? [])];
                                next[i] = { ...next[i], var: e.target.value };
                                updateStepConfig('extractions', next);
                              }}
                              style={{ ...inputStyle, flex: 1, fontSize: '11px' }}
                              placeholder="var_name"
                            />
                            <button
                              onClick={() => {
                                const next = ((cfg.extractions as {var: string; json_path: string}[]) ?? []).filter((_, j) => j !== i);
                                updateStepConfig('extractions', next);
                              }}
                              style={{ background: 'transparent', border: 'none', color: '#f87171', cursor: 'pointer', fontSize: '14px', padding: '0 4px' }}
                            >×</button>
                          </div>
                        ))}
                        <button
                          onClick={() => {
                            const next = [...((cfg.extractions as {var: string; json_path: string}[]) ?? []), { json_path: '$.', var: '' }];
                            updateStepConfig('extractions', next);
                          }}
                          style={{ marginTop: 4, background: 'transparent', border: `1px dashed ${C.outline}`, color: C.textMuted, padding: '4px 10px', borderRadius: '4px', cursor: 'pointer', fontSize: '11px', width: '100%' }}
                        >+ Add extraction</button>
                      </div>
                    </>
                  )}

                  {/* ── Config: transform ── */}
                  {d.step_type === 'transform' && (
                    <>
                      <div style={{ color: C.indigo, fontSize: '10px', fontWeight: 700, letterSpacing: '0.08em', marginBottom: '10px' }}>TRANSFORM CONFIG</div>
                      <div style={{ fontSize: 11, color: '#64748b', marginBottom: 10 }}>
                        Each row maps an output variable name to a Go template expression.<br />
                        Use <code style={{ color: C.cyan }}>{'{{.var_name}}'}</code> to reference upstream variables.
                      </div>
                      {Object.entries((cfg.expressions as Record<string, string>) ?? {}).map(([k, v], i) => (
                        <div key={i} style={{ display: 'flex', gap: 4, marginBottom: 6 }}>
                          <input
                            value={k}
                            onChange={e => {
                              const entries = Object.entries((cfg.expressions as Record<string, string>) ?? {});
                              entries[i] = [e.target.value, v];
                              updateStepConfig('expressions', Object.fromEntries(entries));
                            }}
                            style={{ ...inputStyle, flex: '0 0 90px', fontSize: '11px', fontFamily: 'JetBrains Mono, monospace' }}
                            placeholder="output_var"
                          />
                          <input
                            value={v}
                            onChange={e => {
                              const exprs = { ...((cfg.expressions as Record<string, string>) ?? {}), [k]: e.target.value };
                              updateStepConfig('expressions', exprs);
                            }}
                            style={{ ...inputStyle, flex: 1, fontSize: '11px' }}
                            placeholder={'Hello, {{.user_query}}!'}
                          />
                          <button
                            onClick={() => {
                              const exprs = { ...((cfg.expressions as Record<string, string>) ?? {}) };
                              delete exprs[k];
                              updateStepConfig('expressions', exprs);
                            }}
                            style={{ background: 'transparent', border: 'none', color: '#f87171', cursor: 'pointer', fontSize: '14px', padding: '0 4px' }}
                          >×</button>
                        </div>
                      ))}
                      <button
                        onClick={() => {
                          const exprs = { ...((cfg.expressions as Record<string, string>) ?? {}), '': '' };
                          updateStepConfig('expressions', exprs);
                        }}
                        style={{ marginTop: 4, background: 'transparent', border: `1px dashed ${C.outline}`, color: C.textMuted, padding: '4px 10px', borderRadius: '4px', cursor: 'pointer', fontSize: '11px', width: '100%' }}
                      >+ Add expression</button>
                    </>
                  )}

                  {/* ── Config: response ── */}
                  {d.step_type === 'response' && (
                    <>
                      <div style={{ color: C.cyan, fontSize: '10px', fontWeight: 700, letterSpacing: '0.08em', marginBottom: '10px' }}>RESPONSE CONFIG</div>

                      <label style={labelStyle}>From Variable <span style={hint}>pipeline var to return</span></label>
                      <input
                        value={cfgStr('from_var') || 'output'}
                        onChange={e => updateStepConfig('from_var', e.target.value)}
                        style={inputStyle}
                        placeholder="output"
                      />

                      <div style={fieldGap}>
                        <label style={labelStyle}>Media Type</label>
                        <select
                          value={cfgStr('media_type') || 'text/plain'}
                          onChange={e => updateStepConfig('media_type', e.target.value)}
                          style={selectStyle}
                        >
                          <option value="text/plain">text/plain</option>
                          <option value="text/html">text/html</option>
                          <option value="text/markdown">text/markdown</option>
                          <option value="application/json">application/json</option>
                        </select>
                      </div>
                    </>
                  )}

                  {/* ── Not yet implemented steps ── */}
                  {!['input', 'llm', 'http', 'transform', 'response'].includes(d.step_type) && (
                    <div style={{ color: '#64748b', fontSize: '12px', padding: '12px', border: `1px dashed ${C.outline}`, borderRadius: '6px', textAlign: 'center' }}>
                      Config for <strong style={{ color: C.text }}>{d.step_type}</strong> is not yet supported in the builder.
                    </div>
                  )}

                  {/* ── OUTPUTS: live outgoing connections ── */}
                  {d.step_type !== 'response' && (() => {
                    const outVar = d.step_type === 'input'
                      ? ((d.config?.bindings as Record<string,string>)?.text || 'input')
                      : ((d.config?.output_var as string) || 'output');
                    const outEdges = localPipeEdges.filter(e => e.source === selectedNode.id);
                    return (
                      <div style={{ marginTop: '16px' }}>
                        <div style={{ fontSize: '10px', fontWeight: 700, letterSpacing: '0.08em', color: C.green, marginBottom: '6px' }}>
                          OUTPUTS {outEdges.length === 0 && <span style={{ color: C.textMuted, fontWeight: 400 }}>— nothing connected</span>}
                        </div>
                        {outEdges.map(e => {
                          const tgt = localPipeNodes.find(n => n.id === e.target);
                          const tgtData = tgt?.data as unknown as StepData | undefined;
                          const tgtMeta = tgtData ? (STEP_META[tgtData.step_type] ?? { emoji: '🔧', label: tgtData.step_type }) : { emoji: '?', label: 'unknown' };
                          return (
                            <div key={e.id} style={{ display: 'flex', alignItems: 'center', gap: '8px', padding: '6px 8px', borderRadius: '6px', marginBottom: '4px', background: 'rgba(74,222,128,0.05)', border: '1px solid rgba(74,222,128,0.2)' }}>
                              <code style={{ color: C.green, fontSize: '11px', fontFamily: 'monospace' }}>{`{{${outVar}}}`}</code>
                              <span style={{ color: C.textMuted, fontSize: '11px' }}>→</span>
                              <span style={{ fontSize: '14px' }}>{tgtMeta.emoji}</span>
                              <span style={{ color: '#e2e8f0', fontSize: '11px', fontWeight: 600 }}>{tgtData?.label || tgtMeta.label}</span>
                            </div>
                          );
                        })}
                      </div>
                    );
                  })()}

                </>
              );
            })()}
          </div>
        )}
      </div>
    </div>
  );
}

// ── Page (top-level component) ────────────────────────────────────────────────

export default function AgentBuilderPage() {
  return (
    <AuthGuard>
      <div style={{ display: 'flex', minHeight: '100vh', background: 'var(--tm-bg)' }}>
        <Sidebar />
        <div style={{ marginLeft: 260, flex: 1, display: 'flex', flexDirection: 'column', height: '100vh', overflow: 'hidden' }}>
          <ReactFlowProvider>
            <CanvasInner />
          </ReactFlowProvider>
        </div>
      </div>
    </AuthGuard>
  );
}
