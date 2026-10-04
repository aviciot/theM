'use client';
import { useState } from 'react';
import { type AppFlowHTTPNodeStatus, type AppFlowHTTPParamStatus } from '@/lib/api';
import { C } from '../constants';
import { Section, sharedField, badge, type SaveBtn } from './RuntimeShared';

const INJECT_MODES = ['header', 'query', 'basic', 'custom_header'] as const;

export function RuntimeAppFlowHTTPSection({
  nodes, saving, msg, saveBtn, onSave,
}: {
  nodes: AppFlowHTTPNodeStatus[];
  saving: string | null;
  msg: Record<string, string>;
  saveBtn: SaveBtn;
  onSave: (nodeId: string, paramKey: string, value: string, injectMode: string, injectHeaderName: string) => void;
}) {
  const [values, setValues] = useState<Record<string, string>>({});
  const [modes, setModes]   = useState<Record<string, string>>({});
  const [headers, setHeaders] = useState<Record<string, string>>({});

  if (nodes.length === 0) return null;
  const f = sharedField;

  function key(nodeId: string, paramKey: string) { return `${nodeId}::${paramKey}`; }

  return (
    <Section title="App Canvas — HTTP Nodes" icon="http" accent="#38bdf8" defaultOpen={false}
      subtitle={`${nodes.length} node${nodes.length !== 1 ? 's' : ''} — API credentials, no re-publish needed`}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
        {nodes.map(node => (
          <div key={node.node_id} style={{ padding: '10px 12px', borderRadius: 8, background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(56,189,248,0.15)' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
              <span style={{ fontSize: 12, fontWeight: 700, color: '#38bdf8', fontFamily: 'JetBrains Mono, monospace' }}>
                {node.method ?? 'HTTP'} {node.node_id}
              </span>
              {node.url_template && (
                <span style={{ fontSize: 11, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', maxWidth: 320 }}>
                  {node.url_template}
                </span>
              )}
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {(node.params ?? []).map((param: AppFlowHTTPParamStatus) => {
                const k = key(node.node_id, param.param_key);
                const val = values[k] ?? '';
                const mode = modes[k] ?? param.inject_mode ?? 'header';
                const headerName = headers[k] ?? param.inject_header_name ?? '';
                const isBusy = saving === k;
                const nodeMsg = msg[k] ?? '';
                const isErr = nodeMsg && nodeMsg !== 'Saved';
                const label = param.param_key === 'bearer_token' ? 'Bearer Token' : 'API Key';
                return (
                  <div key={param.param_key} style={{ display: 'flex', flexDirection: 'column', gap: 6, padding: '8px 10px', borderRadius: 6, background: 'rgba(0,0,0,0.18)', border: '1px solid rgba(255,255,255,0.05)' }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                      <span style={{ fontSize: 11, fontWeight: 600, color: C.text, flex: 1 }}>{label}</span>
                      {param.is_set && badge('#4ade80', 'rgba(74,222,128,0.1)', 'rgba(74,222,128,0.3)', 'set')}
                      {!param.is_set && badge('#f87171', 'rgba(248,113,113,0.1)', 'rgba(248,113,113,0.3)', 'not set')}
                    </div>
                    <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
                      <input
                        type="password"
                        placeholder={param.is_set ? '••••••••' : `Enter ${label}`}
                        value={val}
                        onChange={e => setValues(prev => ({ ...prev, [k]: e.target.value }))}
                        style={{ ...f, flex: 1, fontFamily: 'JetBrains Mono, monospace', fontSize: 12 }}
                        autoComplete="new-password"
                      />
                      {param.param_key === 'api_key' && (
                        <select value={mode}
                          onChange={e => setModes(prev => ({ ...prev, [k]: e.target.value }))}
                          style={{ ...f, width: 130, flexShrink: 0 }}>
                          {INJECT_MODES.map(m => <option key={m} value={m}>{m}</option>)}
                        </select>
                      )}
                      {saveBtn(() => onSave(node.node_id, param.param_key, val, mode, headerName), isBusy, !val)}
                    </div>
                    {mode === 'custom_header' && param.param_key === 'api_key' && (
                      <input
                        type="text"
                        placeholder="Header name, e.g. X-API-Key"
                        value={headerName}
                        onChange={e => setHeaders(prev => ({ ...prev, [k]: e.target.value }))}
                        style={{ ...f, fontSize: 12 }}
                      />
                    )}
                    {nodeMsg && <div style={{ fontSize: 12, color: isErr ? C.error : C.green, fontWeight: 600 }}>{nodeMsg}</div>}
                  </div>
                );
              })}
            </div>
          </div>
        ))}
      </div>
    </Section>
  );
}
