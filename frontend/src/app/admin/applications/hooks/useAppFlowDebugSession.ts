'use client';
import { useCallback, useRef, useState } from 'react';
import { themApi } from '@/lib/api';
import { getNodeDef } from '@/lib/nodeRegistry';
import { getBridgeWs } from '../../playground/playgroundTypes';
import type { AppFlowDebugNodeState, AppFlowRuntimeParamSpec, AppFlowLLMCredentialValue } from '../types';
import type { Node, Edge } from '@xyflow/react';

// useAppFlowDebugSession — App Canvas Debug Mode (docs/APP_CANVAS_DEBUG_PLAN.md
// Phase 5). Unlike the agent builder's useDebugSession.ts (a full client-side
// simulator), this is a real WS consumer: POST /admin/applications/{id}/debug/start
// runs the application's saved draft on the debug Temporal worker pool, then this
// hook subscribes to that run's node_start/node_done/node_error events over the
// existing /ws/dashboard connection (same channel-subscribe pattern the playground's
// openDashWs already uses for `run:{runID}`).

export interface AppFlowDebugSessionState {
  active: boolean;
  running: boolean;
  runId: string | null;
  // ISO-8601 — when this run will be forcibly terminated (enforced
  // WorkflowRunTimeout, docs/APPFLOW_RUNTIME_PARAMS_PLAN.md). null until a
  // run has actually started.
  expiresAt: string | null;
  // Temporal workflow ID ("appflow:{tenant}:{run}") for this run — lets the
  // panel deep-link to the Temporal Web UI. null until a run has started.
  workflowId: string | null;
  entryPointSlug: string;
  userMessage: string;
  // Step controls (docs/APP_CANVAS_DEBUG_PLAN.md Phase 6) — set before
  // starting a run; the backend pauses before every node's tick instead of
  // running straight through. Locked once a run has started (same pattern
  // as entryPointSlug/userMessage being disabled while debug.running).
  stepMode: boolean;
  // Per-node LLM credential picker values, keyed by specKey
  // (`${nodeId}:${paramKey}`) — never merged/deduped across nodes, per
  // docs/APPFLOW_RUNTIME_PARAMS_PLAN.md.
  credentials: Record<string, AppFlowLLMCredentialValue>;
  nodeStates: Record<string, AppFlowDebugNodeState>;
  nodeDetails: Record<string, string>;
  nodeErrors: Record<string, string>;
  error: string | null;
  done: boolean;
}

const INITIAL_STATE: AppFlowDebugSessionState = {
  active: false,
  running: false,
  runId: null,
  expiresAt: null,
  workflowId: null,
  entryPointSlug: '',
  userMessage: '',
  stepMode: false,
  credentials: {},
  nodeStates: {},
  nodeDetails: {},
  nodeErrors: {},
  error: null,
  done: false,
};

export function useAppFlowDebugSession({ appId, nodes }: { appId: string; nodes: Node[] }) {
  const [debug, setDebug] = useState<AppFlowDebugSessionState>(INITIAL_STATE);
  const wsRef = useRef<WebSocket | null>(null);

  const entryPointOptions = nodes
    .filter(n => n.type === 'entryPoint')
    .map(n => (n.data as unknown as { slug?: string }).slug)
    .filter((slug): slug is string => !!slug);

  // Generic declared-runtime-param scan (docs/APPFLOW_RUNTIME_PARAMS_PLAN.md):
  // walk every canvas node, and for each one declared runtime param (today
  // just the "llm" kind's llm_credential), produce one spec per NODE, never
  // deduped by param key across nodes — two llm nodes are two independent
  // choices, not "the same secret used twice" the way an HTTP node's shared
  // app_param_key legitimately is in the agent builder.
  const runtimeParamSpecs: AppFlowRuntimeParamSpec[] = nodes.flatMap(n => {
    const nodeType = (n.data as { node_type?: string }).node_type;
    if (!nodeType) return [];
    const decl = getNodeDef(nodeType, 'appflow');
    return (decl.app_params ?? []).map(p => ({
      specKey: `${n.id}:${p.key}`,
      key: p.key,
      label: p.label,
      description: p.description,
      type: p.type,
      required: p.required,
      nodeId: n.id,
      nodeLabel: (n.data as { display_name?: string }).display_name,
    }));
  });

  function openPanel() {
    setDebug(prev => ({
      ...INITIAL_STATE,
      active: true,
      entryPointSlug: prev.entryPointSlug || entryPointOptions[0] || '',
      userMessage: prev.userMessage,
      stepMode: prev.stepMode,
      credentials: prev.credentials,
    }));
  }

  function closePanel() {
    wsRef.current?.close();
    wsRef.current = null;
    setDebug(INITIAL_STATE);
  }

  function setEntryPointSlug(slug: string) {
    setDebug(prev => ({ ...prev, entryPointSlug: slug }));
  }

  function setCredential(specKey: string, value: AppFlowLLMCredentialValue) {
    setDebug(prev => ({ ...prev, credentials: { ...prev.credentials, [specKey]: value } }));
  }

  function setStepMode(stepMode: boolean) {
    setDebug(prev => ({ ...prev, stepMode }));
  }

  function setUserMessage(msg: string) {
    setDebug(prev => ({ ...prev, userMessage: msg }));
  }

  const runAll = useCallback(async () => {
    if (!debug.entryPointSlug.trim()) {
      setDebug(prev => ({ ...prev, error: 'Select an entry point to debug.' }));
      return;
    }

    // Client-side mirror of the server's own required-param validation
    // (docs/APPFLOW_RUNTIME_PARAMS_PLAN.md: "validate required settings
    // server-side before starting the run") — this check is for fast UX
    // feedback only; the server re-validates and is the actual enforcement
    // point, so this must never be treated as sufficient on its own.
    const llmOverrides: Record<string, import('@/lib/api').AppFlowLLMOverrideInput> = {};
    for (const spec of runtimeParamSpecs) {
      if (spec.type !== 'llm_credential') continue;
      const val = debug.credentials[spec.specKey];
      if (!val) {
        if (spec.required) {
          setDebug(prev => ({ ...prev, error: `${spec.nodeLabel || spec.nodeId}: select a provider/key for this node before running.` }));
          return;
        }
        continue;
      }
      llmOverrides[spec.nodeId] = val.mode === 'general'
        ? { mode: 'general', provider: val.provider, key_id: val.keyId, model: val.model || undefined }
        : { mode: 'custom', provider: val.provider, model: val.model, api_key: val.apiKey, base_url: val.baseUrl };
    }

    setDebug(prev => ({
      ...prev, running: true, error: null, done: false, runId: null, expiresAt: null, workflowId: null,
      nodeStates: {}, nodeDetails: {}, nodeErrors: {},
    }));

    try {
      // POST debug/start first to get run_id, then subscribe — the /ws/dashboard
      // protocol only reads one subscribe message per connection (no adding
      // channels later), so the channel name must be known before connecting.
      // This is safe even for a fast-finishing run: the server tails the run's
      // Redis Stream from the beginning on subscribe (runstream.StreamFromRedis
      // replay+live), not a snapshot-only read, so no event is missed even if
      // the run has already finished by the time this WS opens.
      const { run_id, expires_at, workflow_id } = await themApi.startAppFlowDebug(appId, debug.entryPointSlug, debug.userMessage, llmOverrides, debug.stepMode);
      setDebug(prev => ({ ...prev, runId: run_id, expiresAt: expires_at, workflowId: workflow_id }));

      const r = await fetch('/api/auth/token');
      if (!r.ok) throw new Error('Could not get auth token for the debug WS connection.');
      const { token } = await r.json();

      const ws = new WebSocket(`${getBridgeWs()}/ws/dashboard?token=${encodeURIComponent(token)}`);
      wsRef.current = ws;
      ws.onopen = () => { ws.send(JSON.stringify({ type: 'subscribe', channels: [`run:${run_id}`] })); };
      ws.onmessage = (e) => {
        let msg: Record<string, unknown>;
        try { msg = JSON.parse(e.data); } catch { return; }
        // Real trace events arrive as {"channel":"run:...","event":{"type":"node_start",...}}
        // — no top-level "type" field at all (only ping/error/subscribed control messages
        // have one). Gating on msg.type here previously dropped every real event.
        if (msg.type === 'ping' || msg.type === 'subscribed') return;
        if (msg.type === 'error' && !msg.event) {
          // WS-protocol-level error (malformed subscribe, no valid channels) — has no
          // "event" wrapper, unlike a run-level error event nested under msg.event below.
          const message = (msg.message as string) ?? 'Debug WebSocket protocol error';
          setDebug(prev => ({ ...prev, running: false, error: message }));
          ws.close();
          return;
        }
        const ev = msg.event as Record<string, unknown> | undefined;
        if (!ev) return;
        const evType = ev.type as string | undefined;

        if (evType === 'node_start') {
          const nodeId = ev.node_id as string;
          setDebug(prev => ({ ...prev, nodeStates: { ...prev.nodeStates, [nodeId]: 'running' } }));
        } else if (evType === 'node_paused') {
          // Step controls (docs/APP_CANVAS_DEBUG_PLAN.md Phase 6) — the
          // backend has genuinely stopped at this node, waiting for the next
          // Step click. Distinct from 'pending': this is an observed state,
          // not a UI guess about what's next.
          const nodeId = ev.node_id as string;
          setDebug(prev => ({ ...prev, nodeStates: { ...prev.nodeStates, [nodeId]: 'paused' } }));
        } else if (evType === 'node_done') {
          const nodeId = ev.node_id as string;
          const detail = (ev.detail as string) ?? '';
          setDebug(prev => ({
            ...prev,
            nodeStates: { ...prev.nodeStates, [nodeId]: 'done' },
            nodeDetails: { ...prev.nodeDetails, [nodeId]: detail },
          }));
        } else if (evType === 'node_error') {
          const nodeId = ev.node_id as string;
          const detail = (ev.detail as string) ?? 'error';
          setDebug(prev => ({
            ...prev,
            nodeStates: { ...prev.nodeStates, [nodeId]: 'error' },
            nodeErrors: { ...prev.nodeErrors, [nodeId]: detail },
          }));
        } else if (evType === 'done') {
          setDebug(prev => ({ ...prev, running: false, done: true }));
          ws.close();
        } else if (evType === 'error') {
          const message = (ev.message as string) ?? 'Run failed';
          setDebug(prev => ({ ...prev, running: false, error: message }));
          ws.close();
        }
      };
      ws.onerror = () => {
        setDebug(prev => ({ ...prev, running: false, error: 'Debug WebSocket connection error.' }));
      };
      ws.onclose = () => { wsRef.current = null; };
    } catch (e: unknown) {
      const message = e instanceof Error ? e.message : 'Debug run failed to start';
      setDebug(prev => ({ ...prev, running: false, error: message }));
      wsRef.current?.close();
      wsRef.current = null;
    }
  }, [appId, debug.entryPointSlug, debug.userMessage, debug.stepMode, debug.credentials, runtimeParamSpecs]);

  // Sends one Step signal (docs/APP_CANVAS_DEBUG_PLAN.md Phase 6) — releases
  // every node currently paused, in lockstep, by exactly one tick. No local
  // state changes here beyond clearing any stale error: the real state
  // transition arrives asynchronously as node_start/node_done/node_paused
  // events over the WS, same as every other debug state change.
  const step = useCallback(async () => {
    if (!debug.runId) return;
    try {
      await themApi.stepAppFlowDebug(appId, debug.runId);
      setDebug(prev => ({ ...prev, error: null }));
    } catch (e: unknown) {
      const message = e instanceof Error ? e.message : 'Step signal failed';
      setDebug(prev => ({ ...prev, error: message }));
    }
  }, [appId, debug.runId]);

  const reset = useCallback(() => {
    wsRef.current?.close();
    wsRef.current = null;
    setDebug(prev => ({
      ...INITIAL_STATE, active: prev.active,
      entryPointSlug: prev.entryPointSlug, userMessage: prev.userMessage,
      stepMode: prev.stepMode, credentials: prev.credentials,
    }));
  }, []);

  // Decorates inline/flow-control/agent nodes with `_debug` for
  // CanvasNodes.tsx's overlay — kept here (not inline in
  // CanvasBuilderView.tsx) since that file is already over the 400-line
  // file-size guideline. Agent nodes were missing from this list entirely
  // until Platform-as-Tenant Phase 6's live walkthrough found it: an
  // agent-kind node completed a real debug run but never showed any state
  // change at all on the canvas — not a timing issue, decorateNodes simply
  // never applied `_debug` to `type === 'agent'` nodes in the first place.
  const decorateNodes = useCallback((baseNodes: Node[]): Node[] => {
    if (!debug.active) return baseNodes;
    return baseNodes.map(n => {
      if (n.type !== 'inline' && n.type !== 'flowControl' && n.type !== 'agent') return n;
      const state = debug.nodeStates[n.id];
      if (!state) return n;
      return {
        ...n,
        data: {
          ...n.data,
          _debug: { state, detail: debug.nodeDetails[n.id], error: debug.nodeErrors[n.id] },
        },
      };
    });
  }, [debug]);

  // Decorates edges so the taken path lights up as the run progresses — the
  // user asked for this explicitly after a live walkthrough where a
  // completed run gave no visual sense of which wire was actually followed.
  // An edge is "active" once its source node has reported real progress
  // (running/paused/done/error — anything past idle) AND, for a
  // condition/router edge carrying a branch label (sourceHandle
  // "ctrl-out-{label}" or data.label — see CanvasHelpers.ts's canvasToDoc),
  // that label matches the source node's own node_done detail
  // ("branch=<label>"). A plain (unlabeled) edge has only one candidate
  // target, so no branch match is needed — it lights up as soon as its
  // source has run at all.
  const decorateEdges = useCallback((baseEdges: Edge[]): Edge[] => {
    if (!debug.active) return baseEdges;
    return baseEdges.map(e => {
      const sourceState = debug.nodeStates[e.source];
      if (!sourceState || sourceState === 'idle' || sourceState === 'pending') return e;
      const branchLabel = e.sourceHandle?.startsWith('ctrl-out-')
        ? e.sourceHandle.slice('ctrl-out-'.length)
        : (e.data as Record<string, unknown> | undefined)?.label as string | undefined;
      if (branchLabel) {
        const detail = debug.nodeDetails[e.source] ?? '';
        const takenBranch = detail.startsWith('branch=') ? detail.slice('branch='.length) : undefined;
        if (takenBranch !== branchLabel) return e;
      }
      const active = sourceState === 'done' || sourceState === 'running' || sourceState === 'paused';
      if (!active) return e;
      return {
        ...e,
        animated: true,
        style: { ...e.style, stroke: '#4ade80', strokeWidth: 2.5 },
        // styledEdges (CanvasInner.tsx) unconditionally overwrites
        // animated/style on every edge for its own chain-highlighting
        // purpose — this marker tells it to leave this edge's debug styling
        // alone instead of clobbering it (found live: without this, the
        // edge highlight computed here never actually reached the canvas).
        data: { ...e.data, _debugActive: true },
      };
    });
  }, [debug]);

  return {
    debug,
    entryPointOptions,
    runtimeParamSpecs,
    openPanel,
    closePanel,
    setEntryPointSlug,
    setUserMessage,
    setCredential,
    setStepMode,
    runAll,
    step,
    reset,
    decorateNodes,
    decorateEdges,
  };
}
