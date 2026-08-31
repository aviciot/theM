'use client';
import type { Node, Edge } from '@xyflow/react';
import type { Application, EntryPointData, OrchestratorData, EntryPointType } from '../../types';
import { C } from '../../constants';
import { labelStyle, inputStyle, fieldWrap } from './panelStyles';

interface Props {
  selectedNode: Node;
  onUpdateNode: (id: string, data: Record<string, unknown>) => void;
  slugLocked: boolean;
  onSlugManualEdit: () => void;
  app: Application | null;
  nodes: Node[];
  edges: Edge[];
}

export function EntryPointPanel({ selectedNode, onUpdateNode, slugLocked, onSlugManualEdit, app, nodes, edges }: Props) {
  const d = selectedNode.data as EntryPointData;
  const orchEdge = edges.find((e: Edge) => e.source === selectedNode.id);
  const orchNode = orchEdge ? nodes.find((nd: Node) => nd.id === orchEdge.target && nd.type === 'orchestrator') : undefined;
  const orchName = orchNode ? (orchNode.data as OrchestratorData).name : '';
  const isSaved = !!(app?.entry_points?.find((ep: { slug: string }) => ep.slug === d.slug));
  const testUrl = d.epType === 'voice' || d.epType === 'webrtc'
    ? `/apps/${d.slug}/voice`
    : orchName ? `/admin/playground?orchestrator=${encodeURIComponent(orchName)}` : '/admin/playground';

  function endpointUrl() {
    const as = app?.slug ?? '<app-slug>';
    if (d.epType === 'websocket') return `ws://<host>/apps/${as}/${d.slug}/ws`;
    if (d.epType === 'webrtc')   return `http://<host>/apps/${as}/${d.slug}/voice/chat`;
    if (d.epType === 'voice')    return `http://<host>/apps/${as}/${d.slug}/voice/tts`;
    if (d.epType === 'a2a')      return `http://<host>/a2a/${as}/${d.slug}`;
    return `http://<host>/apps/${as}/${d.slug}/sse`;
  }

  function copyEndpointUrl() {
    const as = app?.slug ?? '';
    const host = typeof window !== 'undefined' ? `${window.location.protocol}//${window.location.host}` : 'http://localhost:8088';
    const url = d.epType === 'websocket'
      ? `${host.replace(/^http/, 'ws')}/apps/${as}/${d.slug}/ws`
      : d.epType === 'webrtc'
      ? `${host}/apps/${as}/${d.slug}/voice/chat`
      : d.epType === 'voice'
      ? `${host}/apps/${as}/${d.slug}/voice/tts`
      : d.epType === 'a2a'
      ? `${host}/a2a/${as}/${d.slug}`
      : `${host}/apps/${as}/${d.slug}/sse`;
    navigator.clipboard.writeText(url);
  }

  return (
    <div>
      <div style={fieldWrap}>
        <label style={labelStyle}>Display Name</label>
        <input style={inputStyle} value={d.appName ?? d.label} onChange={e => onUpdateNode(selectedNode.id, { appName: e.target.value, label: e.target.value })} placeholder="e.g. Customer Support" />
      </div>
      <div style={fieldWrap}>
        <label style={labelStyle}>Token Limit <span style={{ fontSize: 10, color: '#64748b' }}>per session · blank = unlimited</span></label>
        <input type="number" min={1} style={inputStyle} value={d.convTokenLimit ?? ''} onChange={e => onUpdateNode(selectedNode.id, { convTokenLimit: e.target.value })} placeholder="e.g. 50000" />
      </div>
      <div style={fieldWrap}>
        <label style={labelStyle}>Max Concurrent Sessions <span style={{ fontSize: 10, color: '#64748b' }}>blank = unlimited</span></label>
        <input type="number" min={1} style={inputStyle} value={d.maxConcurrentSessions ?? ''} onChange={e => onUpdateNode(selectedNode.id, { maxConcurrentSessions: e.target.value })} placeholder="e.g. 10" />
      </div>
      <div style={fieldWrap}>
        <label style={labelStyle}>Queue Timeout (seconds) <span style={{ fontSize: 10, color: '#64748b' }}>blank = reject immediately</span></label>
        <input type="number" min={1} style={inputStyle} value={d.queueTimeout ?? ''} onChange={e => onUpdateNode(selectedNode.id, { queueTimeout: e.target.value })} placeholder="e.g. 60" />
      </div>
      <div style={fieldWrap}>
        <label style={labelStyle}>Queue Message <span style={{ fontSize: 10, color: '#64748b' }}>shown while waiting</span></label>
        <input style={inputStyle} value={d.queueMessage ?? ''} onChange={e => onUpdateNode(selectedNode.id, { queueMessage: e.target.value })} placeholder="All agents are busy, please wait..." />
      </div>
      <div style={fieldWrap}>
        <label style={labelStyle}>Type</label>
        <select style={{ ...inputStyle }} value={d.epType} onChange={e => onUpdateNode(selectedNode.id, { epType: e.target.value as EntryPointType })}>
          <option value="websocket">WebSocket</option>
          <option value="sse">SSE</option>
          <option value="webrtc">WebRTC Voice</option>
          <option value="voice">Voice (STT/TTS)</option>
        </select>
      </div>
      <div style={fieldWrap}>
        <label style={labelStyle}>Access Policy</label>
        <select style={{ ...inputStyle }} value={d.accessMode} onChange={e => onUpdateNode(selectedNode.id, { accessMode: e.target.value as 'token' | 'public' })}>
          <option value="token">Token required</option>
          <option value="public">Public (no auth)</option>
        </select>
      </div>
      <div style={fieldWrap}>
        <label style={{ ...labelStyle, display: 'flex', alignItems: 'center', gap: 6 }}>
          Slug
          {!slugLocked && <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 10, background: 'rgba(0,240,255,0.1)', color: C.cyan, border: '1px solid rgba(0,240,255,0.3)', fontWeight: 600 }}>auto</span>}
        </label>
        <input style={{ ...inputStyle, fontFamily: 'JetBrains Mono, monospace' }} value={d.slug} onChange={e => { onSlugManualEdit(); onUpdateNode(selectedNode.id, { slug: e.target.value }); }} placeholder="my-app-slug" />
        {d.slug && (
          <div style={{
            fontSize: 11, color: C.textMuted, marginTop: 6, padding: '5px 8px',
            background: C.surfaceLow, borderRadius: 5, fontFamily: 'JetBrains Mono, monospace',
            wordBreak: 'break-all', display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 6,
          }}>
            <span style={{ flex: 1 }}>{endpointUrl()}</span>
            <button
              onClick={copyEndpointUrl}
              title="Copy endpoint URL"
              style={{ background: 'none', border: 'none', cursor: 'pointer', color: C.cyan, flexShrink: 0, padding: 0 }}
            >
              <span className="material-symbols-outlined" style={{ fontSize: 14 }}>content_copy</span>
            </button>
          </div>
        )}
      </div>
      <div style={{ marginTop: 12 }}>
        <button
          disabled={!isSaved}
          onClick={() => { if (isSaved) window.open(testUrl, '_blank', 'noopener'); }}
          title={isSaved ? 'Open test interface' : 'Save the application first to enable testing'}
          style={{
            width: '100%', padding: '8px 0', borderRadius: 8, border: `1px solid ${isSaved ? C.green : C.outlineVariant}`,
            background: 'transparent', color: isSaved ? C.green : C.textMuted,
            cursor: isSaved ? 'pointer' : 'not-allowed', fontSize: 13, fontWeight: 600,
            display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 6,
            opacity: isSaved ? 1 : 0.5,
          }}
        >
          <span className="material-symbols-outlined" style={{ fontSize: 15 }}>
            {d.epType === 'voice' ? 'mic' : d.epType === 'webrtc' ? 'videocam' : 'play_arrow'}
          </span>
          Test Entry Point
        </button>
        {!isSaved && (
          <div style={{ fontSize: 10, color: C.textMuted, textAlign: 'center', marginTop: 4 }}>
            Save the application first to enable testing
          </div>
        )}
      </div>
    </div>
  );
}
