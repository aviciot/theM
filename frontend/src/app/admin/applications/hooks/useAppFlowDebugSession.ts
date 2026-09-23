'use client';
import { useCallback, useRef, useState } from 'react';
import { themApi } from '@/lib/api';
import { getBridgeWs } from '../../playground/playgroundTypes';
import type { AppFlowDebugNodeState } from '../types';
import type { Node } from '@xyflow/react';

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
  entryPointSlug: string;
  userMessage: string;
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
  entryPointSlug: '',
  userMessage: '',
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

  function openPanel() {
    setDebug(prev => ({
      ...INITIAL_STATE,
      active: true,
      entryPointSlug: prev.entryPointSlug || entryPointOptions[0] || '',
      userMessage: prev.userMessage,
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

  function setUserMessage(msg: string) {
    setDebug(prev => ({ ...prev, userMessage: msg }));
  }

  const runAll = useCallback(async () => {
    if (!debug.entryPointSlug.trim()) {
      setDebug(prev => ({ ...prev, error: 'Select an entry point to debug.' }));
      return;
    }
    setDebug(prev => ({
      ...prev, running: true, error: null, done: false, runId: null,
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
      const { run_id } = await themApi.startAppFlowDebug(appId, debug.entryPointSlug, debug.userMessage);
      setDebug(prev => ({ ...prev, runId: run_id }));

      const r = await fetch('/api/auth/token');
      if (!r.ok) throw new Error('Could not get auth token for the debug WS connection.');
      const { token } = await r.json();

      const ws = new WebSocket(`${getBridgeWs()}/ws/dashboard?token=${encodeURIComponent(token)}`);
      wsRef.current = ws;
      ws.onopen = () => { ws.send(JSON.stringify({ type: 'subscribe', channels: [`run:${run_id}`] })); };
      ws.onmessage = (e) => {
        let msg: Record<string, unknown>;
        try { msg = JSON.parse(e.data); } catch { return; }
        const type = msg.type as string | undefined;
        if (!type || type === 'ping' || type === 'subscribed') return;
        const ev = (msg.event as Record<string, unknown> | undefined) ?? msg;
        const evType = ev.type as string | undefined;

        if (evType === 'node_start') {
          const nodeId = ev.node_id as string;
          setDebug(prev => ({ ...prev, nodeStates: { ...prev.nodeStates, [nodeId]: 'running' } }));
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
  }, [appId, debug.entryPointSlug, debug.userMessage]);

  const reset = useCallback(() => {
    wsRef.current?.close();
    wsRef.current = null;
    setDebug(prev => ({
      ...INITIAL_STATE, active: prev.active,
      entryPointSlug: prev.entryPointSlug, userMessage: prev.userMessage,
    }));
  }, []);

  // Decorates inline/flow-control nodes with `_debug` for CanvasNodes.tsx's
  // overlay — kept here (not inline in CanvasBuilderView.tsx) since that file
  // is already over the 400-line file-size guideline.
  const decorateNodes = useCallback((baseNodes: Node[]): Node[] => {
    if (!debug.active) return baseNodes;
    return baseNodes.map(n => {
      if (n.type !== 'inline' && n.type !== 'flowControl') return n;
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

  return {
    debug,
    entryPointOptions,
    openPanel,
    closePanel,
    setEntryPointSlug,
    setUserMessage,
    runAll,
    reset,
    decorateNodes,
  };
}
