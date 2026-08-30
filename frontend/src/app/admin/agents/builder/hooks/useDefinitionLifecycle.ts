import { useEffect, useRef, useState } from 'react';
import { useRouter } from 'next/navigation';
import {
  themApi,
  type AgentDefinitionDoc,
  type AgentSkillDoc,
  type AgentStepDoc,
} from '@/lib/api';
import { getNodeDef, fetchNodeTypes, setCachedNodeTypes } from '@/lib/nodeRegistry';
import { INITIAL_VALIDATION } from '../constants';
import { stepMeta } from '../components/StepNode';
import { isDataEdge } from '../canvas/connections';
import type { AgentRootData, SkillData, StepData, StepPolicyOverride, ValidationState } from '../types';
import type { Node, Edge } from '@xyflow/react';

// Re-export for external use
export type { ValidationState };

interface UseDefinitionLifecycleParams {
  defId: string | null;
  router: ReturnType<typeof useRouter>;
  agentSlug: string;
  setAgentSlug: (v: string) => void;
  agentNodes: Node[];
  setAgentNodes: (fn: (prev: Node[]) => Node[]) => void;
  agentEdges: Edge[];
  setAgentEdges: (edges: Edge[]) => void;
  displayName: string;
  setDisplayName: (v: string) => void;
  description: string;
  setDescription: (v: string) => void;
  version: string;
  setVersion: (v: string) => void;
  activeSkillId: string | null;
  skillPipelines: Record<string, { nodes: Node[]; edges: Edge[] }>;
  setSkillPipelines: (fn: (prev: Record<string, { nodes: Node[]; edges: Edge[] }>) => Record<string, { nodes: Node[]; edges: Edge[] }>) => void;
  localPipeNodes: Node[];
  setLocalPipeNodes: (fn: (prev: Node[]) => Node[]) => void;
  localPipeEdges: Edge[];
  savePipelineState: () => void;
  setSelectedNode: (n: Node | null) => void;
  markDirty: () => void;
  setDirty: (v: boolean) => void;
}

export function useDefinitionLifecycle({
  defId,
  router,
  agentSlug,
  setAgentSlug,
  agentNodes,
  setAgentNodes,
  agentEdges,
  setAgentEdges,
  displayName,
  setDisplayName,
  description,
  setDescription,
  version,
  setVersion,
  activeSkillId,
  skillPipelines,
  setSkillPipelines,
  localPipeNodes,
  setLocalPipeNodes,
  localPipeEdges,
  savePipelineState,
  setSelectedNode,
  markDirty,
  setDirty,
}: UseDefinitionLifecycleParams) {
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [validating, setValidating] = useState(false);
  const [publishing, setPublishing] = useState(false);
  const [saveError, setSaveError] = useState('');
  const [loadError, setLoadError] = useState('');
  const [publishError, setPublishError] = useState('');
  const [publishedRevision, setPublishedRevision] = useState<number | null>(null);
  const [dirty, setDirtyLocal] = useState(false);
  const [logoResult, setLogoResult] = useState<'none' | 'valid' | 'invalid' | 'warn'>('none');
  const [nodeTypesReady, setNodeTypesReady] = useState(false);
  const [validation, setValidation] = useState<ValidationState>(INITIAL_VALIDATION);
  const [executionBackend, setExecutionBackend] = useState<'local' | 'temporal'>('local');

  const validationTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const abortRef = useRef<AbortController | null>(null);
  const importFileRef = useRef<HTMLInputElement>(null);

  const combinedSetDirty = (v: boolean) => {
    setDirtyLocal(v);
    setDirty(v);
  };

  useEffect(() => {
    fetchNodeTypes()
      .then(defs => {
        setCachedNodeTypes(defs);
        setAgentNodes(ns => ns.map(n => ({ ...n })));
        setLocalPipeNodes(ns => ns.map(n => ({ ...n })));
        setNodeTypesReady(true);
      })
      .catch(() => { setNodeTypesReady(true); });
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!defId) return;

    if (validationTimerRef.current) clearTimeout(validationTimerRef.current);

    validationTimerRef.current = setTimeout(() => {
      if (abortRef.current) abortRef.current.abort();
      const ctrl = new AbortController();
      abortRef.current = ctrl;

      const rootNodeData = agentNodes.find(n => n.id === 'agent-root')?.data as unknown as AgentRootData | undefined;
      const skills: AgentSkillDoc[] = agentNodes
        .filter(n => n.type === 'skill')
        .map(n => {
          const sd = n.data as unknown as SkillData;
          const pipeline = (sd.skill_id === activeSkillId
            ? { nodes: localPipeNodes, edges: localPipeEdges }
            : skillPipelines[sd.skill_id]) ?? { nodes: [], edges: [] };
          const steps: AgentStepDoc[] = pipeline.nodes.map(sn => {
            const stepd = sn.data as unknown as StepData;
            const ctrlOut = pipeline.edges.filter(e => e.source === sn.id && !isDataEdge(e));
            const defaultLabel = stepMeta(stepd.step_type).label;
            const ctrlPortDefs = getNodeDef(stepd.step_type).control_output_ports ?? [];
            let next = ctrlPortDefs.length > 0
              ? ctrlPortDefs
                  .map(p => ctrlOut.find(e => e.sourceHandle === `ctrl-out-${p.id}`)?.target?.replace('step-', '') ?? '')
                  .filter(Boolean)
              : ctrlOut.map(e => (e.target as string).replace('step-', ''));
            const dataIn = pipeline.edges.filter(e => e.target === sn.id && isDataEdge(e));
            const inputs: Record<string, { from_step: string; from_port: string }> = {};
            for (const de of dataIn) {
              const portID = de.targetHandle?.replace('data-in-', '');
              const fromPort = de.sourceHandle?.replace('data-out-', '');
              const fromStepID = (de.source as string).replace('step-', '');
              if (portID && fromPort && fromStepID) inputs[portID] = { from_step: fromStepID, from_port: fromPort };
            }
            let config: Record<string, unknown> = stepd.config ?? {};
            if (stepd.step_type === 'loop') {
              const bodyEntryEdge = ctrlOut.find(e => e.sourceHandle === 'ctrl-out-loop-body');
              const bodySteps: string[] = [];
              if (bodyEntryEdge) {
                const entryId = (bodyEntryEdge.target as string).replace('step-', '');
                const visited = new Set<string>();
                const queue = [entryId];
                while (queue.length > 0) {
                  const cur = queue.shift()!;
                  if (visited.has(cur)) continue;
                  visited.add(cur);
                  bodySteps.push(cur);
                  const outEdges = pipeline.edges.filter(e => e.source === `step-${cur}` && !isDataEdge(e));
                  for (const oe of outEdges) {
                    const tid = (oe.target as string).replace('step-', '');
                    if (!visited.has(tid)) queue.push(tid);
                  }
                }
              }
              const doneEdge = ctrlOut.find(e => e.sourceHandle === 'ctrl-out-loop-done');
              next = doneEdge ? [(doneEdge.target as string).replace('step-', '')] : [];
              config = { ...config, body_steps: bodySteps };
            }
            return {
              id: stepd.step_id,
              type: stepd.step_type as AgentStepDoc['type'],
              label: (stepd.label && stepd.label !== defaultLabel) ? stepd.label : undefined,
              config,
              next,
              ...(Object.keys(inputs).length > 0 ? { inputs } : {}),
              ...(stepd.policy ? { policy: stepd.policy } : {}),
              position: sn.position,
            };
          });
          return {
            skill_id: sd.skill_id, name: sd.name, description: sd.description ?? '',
            tags: sd.tags ?? [], input_modes: sd.input_modes ?? ['text/plain'],
            output_modes: sd.output_modes ?? ['text/plain'], examples: sd.examples ?? [],
            input_schema: {}, output_schema: {}, steps, position: n.position,
          };
        });

      const liveDefinition: AgentDefinitionDoc = {
        schema_version: 1,
        agent_slug: agentSlug,
        agent_root: {
          display_name: rootNodeData?.display_name ?? '',
          description: rootNodeData?.description ?? '',
          version: rootNodeData?.version ?? '1.0.0',
          capabilities: { streaming: false, push_notifications: false },
        },
        skills,
      };

      setValidation(prev => ({ ...prev, loading: true }));
      themApi.validateAgentDefinition(defId, liveDefinition, ctrl.signal)
        .then(result => {
          setValidation({ issues: result.issues ?? [], stepContracts: result.step_contracts ?? {}, loading: false, lastValidatedAt: Date.now() });
        })
        .catch(e => {
          if ((e as { name?: string }).name === 'AbortError') return;
          setValidation(prev => ({ ...prev, loading: false }));
        });
    }, 1200);

    return () => {
      if (validationTimerRef.current) clearTimeout(validationTimerRef.current);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [defId, agentNodes, agentEdges, agentSlug, skillPipelines, localPipeNodes, localPipeEdges]);

  useEffect(() => {
    if (!defId) return;
    themApi.getAgentDefinition(defId).then(resp => {
      const doc = resp.definition;
      if (!doc) return;
      setAgentSlug(doc.agent_slug ?? '');
      setDisplayName(doc.agent_root.display_name ?? '');
      setDescription(doc.agent_root.description ?? '');
      setVersion(doc.agent_root.version ?? '1.0.0');
      setExecutionBackend(doc.agent_root.execution_backend === 'temporal' ? 'temporal' : 'local');
      loadDefinitionDoc(doc);
    }).catch(e => {
      setLoadError('Failed to load definition: ' + String(e));
    });
  }, [defId]); // eslint-disable-line react-hooks/exhaustive-deps

  function loadDefinitionDoc(doc: AgentDefinitionDoc) {
    const rootNode: Node = {
      id: 'agent-root',
      type: 'agentRoot',
      position: { x: 300, y: 80 },
      data: {
        display_name: doc.agent_root.display_name,
        description: doc.agent_root.description ?? '',
        version: doc.agent_root.version ?? '1.0.0',
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
    setAgentNodes(() => [rootNode, ...skillNodes]);
    setAgentEdges(skillEdges);

    const pipelines: Record<string, { nodes: Node[]; edges: Edge[] }> = {};
    for (const sk of doc.skills) {
      const stepNodes: Node[] = (sk.steps ?? []).map((step, si) => ({
        id: `step-${step.id}`,
        type: 'step',
        position: step.position ?? { x: 200, y: 80 + si * 120 },
        data: {
          step_id: step.id,
          step_type: step.type,
          label: (step as AgentStepDoc & { label?: string }).label || stepMeta(step.type).label,
          config: (step.config as Record<string, unknown>) ?? {},
          inputs: step.inputs
            ? Object.fromEntries(Object.entries(step.inputs).map(([portID, b]) => [portID, { from_step: b.from_step, from_port: b.from_port }]))
            : undefined,
          ...(step.policy ? { policy: step.policy as StepPolicyOverride } : {}),
        },
      }));
      const stepEdges: Edge[] = [];
      for (const step of (sk.steps ?? [])) {
        if (step.type === 'loop') {
          // loop-done edge: step.next[0] is the post-loop target
          const doneTarget = (step.next ?? [])[0];
          if (doneTarget) {
            stepEdges.push({
              id: `${step.id}-to-${doneTarget}`,
              source: `step-${step.id}`,
              target: `step-${doneTarget}`,
              sourceHandle: 'ctrl-out-loop-done',
            });
          }
          // loop-body edge: first entry in body_steps
          const cfg = (step.config ?? {}) as Record<string, unknown>;
          const bodySteps = cfg.body_steps as string[] | undefined;
          const bodyEntry = bodySteps?.[0];
          if (bodyEntry) {
            stepEdges.push({
              id: `${step.id}-body-to-${bodyEntry}`,
              source: `step-${step.id}`,
              target: `step-${bodyEntry}`,
              sourceHandle: 'ctrl-out-loop-body',
            });
          }
        } else {
          const ctrlPortDefs = getNodeDef(step.type as string).control_output_ports ?? [];
          (step.next ?? []).forEach((nextId, idx) => {
            const sourceHandle = ctrlPortDefs.length > 0
              ? `ctrl-out-${ctrlPortDefs[idx]?.id ?? idx}`
              : undefined;
            stepEdges.push({
              id: `${step.id}-to-${nextId}`,
              source: `step-${step.id}`,
              target: `step-${nextId}`,
              ...(sourceHandle ? { sourceHandle } : {}),
            });
          });
        }
        if (step.inputs) {
          for (const [portID, binding] of Object.entries(step.inputs)) {
            stepEdges.push({
              id: `data-${binding.from_step}-${binding.from_port}-to-${step.id}-${portID}`,
              source: `step-${binding.from_step}`,
              target: `step-${step.id}`,
              sourceHandle: `data-out-${binding.from_port}`,
              targetHandle: `data-in-${portID}`,
              type: 'dataEdge',
              data: { kind: 'data' },
            });
          }
        }
      }
      pipelines[sk.skill_id] = { nodes: stepNodes, edges: stepEdges };
    }
    setSkillPipelines(() => pipelines);
  }

  function buildDefinitionDoc(): AgentDefinitionDoc {
    const rootNodeData = agentNodes.find(n => n.id === 'agent-root')?.data as unknown as AgentRootData | undefined;
    const dn = rootNodeData?.display_name ?? displayName;
    const desc = rootNodeData?.description ?? description;
    const ver = rootNodeData?.version ?? version;

    const skills: AgentSkillDoc[] = agentNodes
      .filter(n => n.type === 'skill')
      .map(n => {
        const sd = n.data as unknown as SkillData;
        const pipeline = skillPipelines[sd.skill_id] ?? { nodes: [], edges: [] };
        const steps: AgentStepDoc[] = pipeline.nodes.map(sn => {
          const stepd = sn.data as unknown as StepData;
          const ctrlOut = pipeline.edges.filter(e => e.source === sn.id && !isDataEdge(e));
          const defaultLabel = stepMeta(stepd.step_type).label;
          const ctrlPortDefs = getNodeDef(stepd.step_type).control_output_ports ?? [];
          let next: string[];
          if (ctrlPortDefs.length > 0) {
            next = ctrlPortDefs
              .map(p => ctrlOut.find(e => e.sourceHandle === `ctrl-out-${p.id}`)?.target?.replace('step-', '') ?? '')
              .filter(Boolean);
          } else {
            next = ctrlOut.map(e => (e.target as string).replace('step-', ''));
          }
          const dataIn = pipeline.edges.filter(e => e.target === sn.id && isDataEdge(e));
          const inputs: Record<string, { from_step: string; from_port: string }> = {};
          for (const de of dataIn) {
            const portID = de.targetHandle?.replace('data-in-', '');
            const fromPort = de.sourceHandle?.replace('data-out-', '');
            const fromStepID = (de.source as string).replace('step-', '');
            if (portID && fromPort && fromStepID) inputs[portID] = { from_step: fromStepID, from_port: fromPort };
          }

          let config: Record<string, unknown> = stepd.config ?? {};
          if (stepd.step_type === 'loop') {
            // Derive body_steps via BFS from the loop-body control edge.
            const bodyEntryEdge = ctrlOut.find(e => e.sourceHandle === 'ctrl-out-loop-body');
            const bodySteps: string[] = [];
            if (bodyEntryEdge) {
              const entryId = (bodyEntryEdge.target as string).replace('step-', '');
              const visited = new Set<string>();
              const queue = [entryId];
              while (queue.length > 0) {
                const cur = queue.shift()!;
                if (visited.has(cur)) continue;
                visited.add(cur);
                bodySteps.push(cur);
                const outEdges = pipeline.edges.filter(e => e.source === `step-${cur}` && !isDataEdge(e));
                for (const oe of outEdges) {
                  const tid = (oe.target as string).replace('step-', '');
                  if (!visited.has(tid)) queue.push(tid);
                }
              }
            }
            // next is only the loop-done target, not body steps.
            const doneEdge = ctrlOut.find(e => e.sourceHandle === 'ctrl-out-loop-done');
            next = doneEdge ? [(doneEdge.target as string).replace('step-', '')] : [];
            config = { ...config, body_steps: bodySteps };
          }

          return {
            id: stepd.step_id,
            type: stepd.step_type as AgentStepDoc['type'],
            label: (stepd.label && stepd.label !== defaultLabel) ? stepd.label : undefined,
            config,
            next,
            ...(Object.keys(inputs).length > 0 ? { inputs } : {}),
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
        ...(executionBackend === 'temporal' ? { execution_backend: 'temporal' as const } : {}),
      },
      skills,
    };
  }

  function handleImportJSON() {
    importFileRef.current?.click();
  }

  function handleImportFileChange(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    if (!file) return;
    const reader = new FileReader();
    reader.onload = (ev) => {
      try {
        const doc = JSON.parse(ev.target?.result as string) as AgentDefinitionDoc;
        if (!doc.agent_root || !Array.isArray(doc.skills)) throw new Error('Missing agent_root or skills array');
        setAgentSlug(doc.agent_slug ?? '');
        loadDefinitionDoc(doc);
        markDirty();
        setSaveError('');
      } catch (err) {
        setSaveError(`Import failed: ${String(err)}`);
      }
      e.target.value = '';
    };
    reader.readAsText(file);
  }

  async function handleSave() {
    if (!agentSlug.trim()) {
      setSaveError('Agent slug is required — fill in the slug field in the toolbar before saving.');
      setLogoResult('invalid');
      setTimeout(() => setLogoResult('none'), 1800);
      return;
    }
    setSaving(true);
    setSaveError('');
    setLogoResult('none');
    savePipelineState();
    try {
      const doc = buildDefinitionDoc();
      if (defId) {
        await themApi.updateAgentDefinition(defId, { definition: doc });
        combinedSetDirty(false);
      } else {
        const result = await themApi.createAgentDefinition({ agent_slug: agentSlug, definition: doc });
        router.replace(`/admin/agents/builder?id=${result.id}`);
        combinedSetDirty(false);
      }
      setLogoResult('valid');
      setTimeout(() => setLogoResult('none'), 1800);
    } catch (e) {
      setSaveError(String(e));
      setLogoResult('invalid');
      setTimeout(() => setLogoResult('none'), 1800);
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
    setLogoResult('none');
    setValidation(prev => ({ ...prev, loading: true }));
    savePipelineState();
    try {
      const result = await themApi.validateAgentDefinition(defId, buildDefinitionDoc());
      setValidation({ issues: result.issues ?? [], stepContracts: result.step_contracts ?? {}, loading: false, lastValidatedAt: Date.now() });
      const errors   = (result.issues ?? []).filter(i => i.severity === 'error').length;
      const warnings = (result.issues ?? []).filter(i => i.severity === 'warning').length;
      const r = errors > 0 ? 'invalid' : warnings > 0 ? 'warn' : 'valid';
      setLogoResult(r);
      setTimeout(() => setLogoResult('none'), 1800);
    } catch {
      setValidation(prev => ({ ...prev, loading: false }));
    } finally {
      setValidating(false);
    }
  }

  async function handlePublish() {
    if (!defId || !confirm('Publish this agent definition? This creates a runtime agent entry.')) return;
    setPublishing(true);
    setPublishError('');
    setLogoResult('none');
    try {
      const result = await themApi.publishAgentDefinition(defId);
      setPublishedRevision(result.revision);
      combinedSetDirty(false);
      setLogoResult('valid');
      setTimeout(() => setLogoResult('none'), 1800);
    } catch (e: unknown) {
      const refreshed = await themApi.validateAgentDefinition(defId);
      if (refreshed.issues && refreshed.issues.length > 0) {
        setValidation({ issues: refreshed.issues, stepContracts: refreshed.step_contracts ?? {}, loading: false, lastValidatedAt: Date.now() });
        setPublishError('Publish failed — fix errors before publishing.');
        setLogoResult('invalid');
      } else {
        setPublishError(String(e));
        setLogoResult('invalid');
      }
      setTimeout(() => setLogoResult('none'), 1800);
    } finally {
      setPublishing(false);
    }
  }

  function handleExport() {
    savePipelineState();
    const doc = buildDefinitionDoc();
    const blob = new Blob([JSON.stringify(doc, null, 2)], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `${agentSlug || 'agent'}.json`;
    a.click();
    URL.revokeObjectURL(url);
  }

  return {
    saving, deleting, validating, publishing,
    saveError, setSaveError,
    loadError,
    publishError,
    publishedRevision,
    dirty, setDirty: combinedSetDirty,
    logoResult, setLogoResult,
    nodeTypesReady,
    validation, setValidation,
    executionBackend, setExecutionBackend,
    importFileRef,
    loadDefinitionDoc,
    buildDefinitionDoc,
    handleSave,
    handleDelete,
    handleValidate,
    handlePublish,
    handleExport,
    handleImportJSON,
    handleImportFileChange,
  };
}
