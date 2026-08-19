'use client';
import { useCallback, useEffect, useState, type MouseEvent } from 'react';
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
}

interface StepData {
  step_id: string;
  step_type: string;
  label: string;
}

// ── Node components (must be outside the render component) ───────────────────

function AgentRootNode({ data }: { data: AgentRootData }) {
  return (
    <div style={{
      background: C.cyanBg, border: `2px solid ${C.cyanBorder}`,
      borderRadius: '12px', padding: '16px', minWidth: '200px',
      boxShadow: '0 0 20px rgba(0,240,255,0.2)',
    }}>
      <Handle type="source" position={Position.Bottom} style={{ background: C.cyan }} />
      <div style={{ color: C.cyan, fontSize: '10px', fontWeight: 700, letterSpacing: '0.1em', marginBottom: '6px' }}>AGENT ROOT</div>
      <div style={{ color: '#fff', fontWeight: 700, fontSize: '14px' }}>{data.display_name || 'Unnamed Agent'}</div>
      {data.description && <div style={{ color: C.textMuted, fontSize: '12px', marginTop: '4px' }}>{data.description}</div>}
      {data.credential_slots.length > 0 && (
        <div style={{ marginTop: '8px', fontSize: '11px', color: C.amber }}>
          {data.credential_slots.length} credential slot{data.credential_slots.length !== 1 ? 's' : ''}
        </div>
      )}
    </div>
  );
}

function SkillNode({ data }: { data: SkillData }) {
  return (
    <div style={{
      background: C.purpleBg, border: `1.5px solid ${C.purpleBorder}`,
      borderRadius: '10px', padding: '12px', minWidth: '160px',
      cursor: 'pointer',
    }}>
      <Handle type="target" position={Position.Top} style={{ background: C.purple }} />
      <Handle type="source" position={Position.Bottom} style={{ background: C.purple }} />
      <div style={{ color: C.purple, fontSize: '10px', fontWeight: 700, letterSpacing: '0.08em', marginBottom: '4px' }}>SKILL</div>
      <div style={{ color: '#fff', fontWeight: 600, fontSize: '13px' }}>{data.name || data.skill_id}</div>
      {data.description && <div style={{ color: C.textMuted, fontSize: '11px', marginTop: '2px' }}>{data.description}</div>}
      <div style={{ marginTop: '6px', fontSize: '10px', color: C.textMuted }}>Double-click to edit pipeline</div>
    </div>
  );
}

function StepNode({ data }: { data: StepData }) {
  const colors: Record<string, { bg: string; border: string; label: string }> = {
    input:      { bg: C.greenBg,  border: C.greenBorder, label: 'INPUT' },
    response:   { bg: C.cyanBg,   border: C.cyanBorder,  label: 'RESPONSE' },
    llm:        { bg: C.purpleBg, border: C.purpleBorder, label: 'LLM' },
    http:       { bg: C.amberBg,  border: C.amberBorder,  label: 'HTTP' },
    transform:  { bg: C.indigoBg, border: C.indigoBorder, label: 'TRANSFORM' },
    branch:     { bg: C.amberBg,  border: C.amberBorder,  label: 'BRANCH' },
    loop:       { bg: C.amberBg,  border: C.amberBorder,  label: 'LOOP' },
    parallel:   { bg: C.purpleBg, border: C.purpleBorder, label: 'PARALLEL' },
    a2a_call:   { bg: C.cyanBg,   border: C.cyanBorder,   label: 'A2A CALL' },
    human_wait: { bg: C.greenBg,  border: C.greenBorder,  label: 'HUMAN WAIT' },
    stream_out: { bg: C.cyanBg,   border: C.cyanBorder,   label: 'STREAM OUT' },
  };
  const style = colors[data.step_type] ?? { bg: C.indigoBg, border: C.indigoBorder, label: data.step_type.toUpperCase() };
  return (
    <div style={{
      background: style.bg, border: `1.5px solid ${style.border}`,
      borderRadius: '8px', padding: '10px 14px', minWidth: '130px',
    }}>
      <Handle type="target" position={Position.Top} style={{ background: style.border }} />
      <Handle type="source" position={Position.Bottom} style={{ background: style.border }} />
      <div style={{ color: style.border, fontSize: '9px', fontWeight: 700, letterSpacing: '0.1em', marginBottom: '2px' }}>{style.label}</div>
      <div style={{ color: '#fff', fontWeight: 600, fontSize: '12px' }}>{data.label || data.step_id}</div>
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

// ── Canvas inner (uses ReactFlow hooks — must be inside ReactFlowProvider) ───

function CanvasInner() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const defId = searchParams.get('id');
  const { screenToFlowPosition } = useReactFlow();

  // View state: 'agent' = top-level, 'skill' = pipeline for a skill
  const [activeView, setActiveView] = useState<'agent' | 'skill'>('agent');
  const [activeSkillId, setActiveSkillId] = useState<string | null>(null);

  // Agent-level nodes/edges
  const [agentNodes, setAgentNodes, onAgentNodesChange] = useNodesState<Node>([]);
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
      data: { skill_id: sk.skill_id, name: sk.name, description: sk.description ?? '' },
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
        data: { step_id: step.id, step_type: step.type, label: step.type },
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
            config: {},
            next: outEdges.map(e => (e.target as string).replace('step-', '')),
            position: sn.position,
          };
        });
        return {
          skill_id: sd.skill_id,
          name: sd.name,
          description: sd.description ?? '',
          tags: [],
          input_modes: ['text'],
          output_modes: ['text'],
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

  function addSkill() {
    const sid = `skill-${Date.now()}`;
    const newNode: Node = {
      id: `skill-${sid}`,
      type: 'skill',
      position: { x: 150 + agentNodes.filter(n => n.type === 'skill').length * 220, y: 250 },
      data: { skill_id: sid, name: 'New Skill', description: '' },
    };
    setAgentNodes(prev => [...prev, newNode]);
    if (agentNodes.find(n => n.id === 'agent-root')) {
      setAgentEdges(prev => [...prev, { id: `root-to-${sid}`, source: 'agent-root', target: `skill-${sid}` }]);
    }
    setDirty(true);
  }

  function addStepToActivePipeline(type: AgentStepDoc['type']) {
    if (!activeSkillId) return;
    const stepId = `step-${Date.now()}`;
    const newNode: Node = {
      id: `step-${stepId}`,
      type: 'step',
      position: screenToFlowPosition({ x: 300, y: 200 }),
      data: { step_id: stepId, step_type: type, label: type },
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

  const onPipeConnect = useCallback((conn: Connection) => {
    setLocalPipeEdges(prev => addEdge(conn, prev));
    setDirty(true);
  }, [setLocalPipeEdges]);

  // Properties panel update
  function updateSelectedNodeField(field: string, value: string) {
    if (!selectedNode) return;
    if (activeView === 'agent') {
      setAgentNodes(prev => prev.map(n =>
        n.id === selectedNode.id ? { ...n, data: { ...n.data, [field]: value } } : n
      ));
    } else {
      setLocalPipeNodes(prev => prev.map(n =>
        n.id === selectedNode.id ? { ...n, data: { ...n.data, [field]: value } } : n
      ));
    }
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
        {/* Left palette */}
        <div style={{
          width: '180px', flexShrink: 0, borderRight: `1px solid ${C.outline}`,
          background: C.surface, padding: '16px 12px', overflowY: 'auto',
        }}>
          {activeView === 'agent' ? (
            <>
              <div style={{ color: C.textMuted, fontSize: '11px', fontWeight: 700, letterSpacing: '0.08em', marginBottom: '10px' }}>ADD</div>
              <button onClick={addSkill} style={{
                width: '100%', background: C.purpleBg, border: `1px solid ${C.purpleBorder}`,
                color: C.purple, padding: '8px', borderRadius: '6px', cursor: 'pointer',
                fontSize: '12px', fontWeight: 600, marginBottom: '6px',
              }}>
                + Skill
              </button>
              <div style={{ color: C.textMuted, fontSize: '11px', marginTop: '16px', marginBottom: '6px', fontWeight: 700, letterSpacing: '0.08em' }}>CREDENTIAL SLOTS</div>
              <button onClick={() => {
                setCredentialSlots(prev => [...prev, { name: `slot_${prev.length + 1}`, description: '', required: true }]);
                setDirty(true);
              }} style={{
                width: '100%', background: C.amberBg, border: `1px solid ${C.amberBorder}`,
                color: C.amber, padding: '8px', borderRadius: '6px', cursor: 'pointer',
                fontSize: '12px', fontWeight: 600,
              }}>
                + Slot
              </button>
              {credentialSlots.map((slot, i) => (
                <div key={i} style={{ marginTop: '6px', background: C.amberBg, border: `1px solid ${C.amberBorder}`, borderRadius: '6px', padding: '6px' }}>
                  <input
                    value={slot.name}
                    onChange={e => {
                      const next = [...credentialSlots];
                      next[i] = { ...next[i], name: e.target.value };
                      setCredentialSlots(next);
                      setDirty(true);
                    }}
                    style={{ width: '100%', background: 'transparent', border: 'none', color: C.amber, fontSize: '11px', outline: 'none' }}
                    placeholder="slot name"
                  />
                </div>
              ))}
            </>
          ) : (
            <>
              <div style={{ color: C.textMuted, fontSize: '11px', fontWeight: 700, letterSpacing: '0.08em', marginBottom: '10px' }}>ADD STEP</div>
              {STEP_TYPES.map(st => (
                <button key={st.type} onClick={() => addStepToActivePipeline(st.type)} style={{
                  width: '100%', background: 'transparent', border: `1px solid ${C.outline}`,
                  color: C.text, padding: '7px 8px', borderRadius: '6px', cursor: 'pointer',
                  fontSize: '12px', marginBottom: '4px', textAlign: 'left',
                }}>
                  {st.label}
                </button>
              ))}
            </>
          )}
        </div>

        {/* Canvas */}
        <div style={{ flex: 1, position: 'relative' }}>
          {activeView === 'agent' ? (
            <ReactFlow
              nodes={currentNodes}
              edges={currentEdges}
              onNodesChange={onAgentNodesChange}
              onEdgesChange={onAgentEdgesChange}
              onConnect={onAgentConnect}
              onNodeDoubleClick={onAgentNodeDoubleClick}
              nodeTypes={nodeTypes}
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
              onNodeDoubleClick={onPipeNodeDoubleClick}
              nodeTypes={nodeTypes}
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
            width: '260px', flexShrink: 0, borderLeft: `1px solid ${C.outline}`,
            background: C.surface, padding: '16px', overflowY: 'auto',
          }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '16px' }}>
              <span style={{ color: C.text, fontWeight: 700, fontSize: '13px' }}>Properties</span>
              <button onClick={() => setSelectedNode(null)} style={{ background: 'transparent', border: 'none', color: C.textMuted, cursor: 'pointer', fontSize: '16px' }}>x</button>
            </div>

            {selectedNode.type === 'agentRoot' && (() => {
              const d = selectedNode.data as unknown as AgentRootData;
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
              const d = selectedNode.data as unknown as SkillData;
              return (
                <>
                  <label style={{ color: C.textMuted, fontSize: '11px', fontWeight: 700, display: 'block', marginBottom: '4px' }}>Skill ID</label>
                  <input
                    value={d.skill_id}
                    onChange={e => updateSelectedNodeField('skill_id', e.target.value)}
                    style={{ width: '100%', background: 'transparent', border: `1px solid ${C.outline}`, color: '#fff', padding: '6px', borderRadius: '4px', fontSize: '13px', boxSizing: 'border-box' }}
                  />
                  <label style={{ color: C.textMuted, fontSize: '11px', fontWeight: 700, display: 'block', marginTop: '12px', marginBottom: '4px' }}>Name</label>
                  <input
                    value={d.name}
                    onChange={e => updateSelectedNodeField('name', e.target.value)}
                    style={{ width: '100%', background: 'transparent', border: `1px solid ${C.outline}`, color: '#fff', padding: '6px', borderRadius: '4px', fontSize: '13px', boxSizing: 'border-box' }}
                  />
                  <label style={{ color: C.textMuted, fontSize: '11px', fontWeight: 700, display: 'block', marginTop: '12px', marginBottom: '4px' }}>Description</label>
                  <textarea
                    value={d.description}
                    onChange={e => updateSelectedNodeField('description', e.target.value)}
                    rows={2}
                    style={{ width: '100%', background: 'transparent', border: `1px solid ${C.outline}`, color: '#fff', padding: '6px', borderRadius: '4px', fontSize: '13px', resize: 'vertical', boxSizing: 'border-box' }}
                  />
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
              const d = selectedNode.data as unknown as StepData;
              return (
                <>
                  <label style={{ color: C.textMuted, fontSize: '11px', fontWeight: 700, display: 'block', marginBottom: '4px' }}>Step ID</label>
                  <input
                    value={d.step_id}
                    onChange={e => updateSelectedNodeField('step_id', e.target.value)}
                    style={{ width: '100%', background: 'transparent', border: `1px solid ${C.outline}`, color: '#fff', padding: '6px', borderRadius: '4px', fontSize: '13px', boxSizing: 'border-box' }}
                  />
                  <label style={{ color: C.textMuted, fontSize: '11px', fontWeight: 700, display: 'block', marginTop: '12px', marginBottom: '4px' }}>Label</label>
                  <input
                    value={d.label}
                    onChange={e => updateSelectedNodeField('label', e.target.value)}
                    style={{ width: '100%', background: 'transparent', border: `1px solid ${C.outline}`, color: '#fff', padding: '6px', borderRadius: '4px', fontSize: '13px', boxSizing: 'border-box' }}
                  />
                  <label style={{ color: C.textMuted, fontSize: '11px', fontWeight: 700, display: 'block', marginTop: '12px', marginBottom: '4px' }}>Type</label>
                  <div style={{ color: C.text, fontSize: '13px', padding: '6px', border: `1px solid ${C.outline}`, borderRadius: '4px' }}>
                    {d.step_type}
                  </div>
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
      <div style={{ display: 'flex', height: '100vh', background: 'var(--tm-bg)' }}>
        <Sidebar />
        <div style={{ flex: 1, overflow: 'hidden', display: 'flex', flexDirection: 'column' }}>
          <ReactFlowProvider>
            <CanvasInner />
          </ReactFlowProvider>
        </div>
      </div>
    </AuthGuard>
  );
}
