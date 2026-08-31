'use client';
import { useState } from 'react';
import { themApi, type MCPServer, type MCPTool } from '@/lib/api';
import { ACCENT } from './mcpConstants';

export function ToolRow({ tool }: { tool: MCPTool }) {
  const [open, setOpen] = useState(false);
  const hasSchema = tool.inputSchema && Object.keys(tool.inputSchema).length > 0;
  return (
    <div style={{ borderRadius: '8px', background: 'var(--tm-inset-deep)', border: '1px solid var(--tm-divider)', overflow: 'hidden' }}>
      <button onClick={() => setOpen(v => !v)} style={{ width: '100%', textAlign: 'left', padding: '9px 12px', background: 'none', border: 'none', cursor: 'pointer', display: 'flex', alignItems: 'center', gap: '8px' }}>
        <span className="material-symbols-outlined" style={{ fontSize: '14px', color: ACCENT, flexShrink: 0 }}>
          {open ? 'expand_less' : 'expand_more'}
        </span>
        <span style={{ fontSize: '12px', fontWeight: 600, color: 'var(--tm-card-text)', fontFamily: 'monospace' }}>{tool.name}</span>
        {tool.description && (
          <span style={{ fontSize: '11px', color: 'var(--tm-card-text-muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', flex: 1 }}>— {tool.description}</span>
        )}
        {hasSchema && (
          <span style={{ fontSize: '9px', fontWeight: 700, padding: '1px 5px', borderRadius: '4px', background: `${ACCENT}20`, border: `1px solid ${ACCENT}40`, color: ACCENT, flexShrink: 0 }}>schema</span>
        )}
      </button>
      {open && hasSchema && (
        <div style={{ padding: '0 12px 10px 34px' }}>
          <pre style={{ fontSize: '10px', color: 'var(--tm-card-text-hint)', margin: 0, background: 'rgba(0,0,0,0.3)', borderRadius: '6px', padding: '8px', overflowX: 'auto', maxHeight: '160px' }}>
            {JSON.stringify(tool.inputSchema, null, 2)}
          </pre>
        </div>
      )}
    </div>
  );
}

export function ProbeButton({ serverId, onDone }: { serverId: string; onDone?: (s: MCPServer) => void }) {
  const [state, setState] = useState<'idle' | 'loading' | 'ok' | 'err'>('idle');
  const [msg, setMsg] = useState('');

  async function run() {
    setState('loading');
    setMsg('');
    try {
      const res = await themApi.probeMCPServer(serverId);
      setMsg(`${res.health_status} · ${res.tools_count} tools`);
      setState('ok');
      if (onDone) {
        const refreshed = await themApi.getMCPServer(serverId);
        onDone(refreshed);
      }
    } catch (e) {
      setMsg((e as Error).message || 'Probe failed');
      setState('err');
    }
  }

  return (
    <div>
      <button onClick={run} disabled={state === 'loading'} style={{ display: 'inline-flex', alignItems: 'center', gap: '6px', padding: '8px 16px', borderRadius: '8px', cursor: state === 'loading' ? 'not-allowed' : 'pointer', background: `${ACCENT}22`, border: `1px solid ${ACCENT}55`, color: ACCENT, fontSize: '12px', fontWeight: 600, transition: 'all 150ms ease' }}>
        <span className={`material-symbols-outlined${state === 'loading' ? ' spin' : ''}`} style={{ fontSize: '14px' }}>
          {state === 'loading' ? 'sync' : 'play_arrow'}
        </span>
        {state === 'loading' ? 'Probing…' : 'Test connection'}
      </button>
      {msg && (
        <div style={{ marginTop: '8px', fontSize: '11px', padding: '6px 10px', borderRadius: '6px', background: state === 'ok' ? 'rgba(16,185,129,0.08)' : 'rgba(220,38,38,0.08)', border: `1px solid ${state === 'ok' ? 'rgba(16,185,129,0.2)' : 'rgba(220,38,38,0.2)'}`, color: state === 'ok' ? '#34d399' : '#f87171' }}>
          {state === 'ok' ? '✓ ' : '✗ '}{msg}
        </div>
      )}
    </div>
  );
}
