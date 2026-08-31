'use client';
import type { Application, ChainStatus, OrchestratorData } from '../../types';
import { C } from '../../constants';

interface Props {
  appName: string;
  onAppNameChange: (name: string) => void;
  convTokenLimit: string;
  onConvTokenLimitChange: (val: string) => void;
  chain: ChainStatus;
  app: Application | null;
  epCount: number;
}

export function AppPanel({ appName, onAppNameChange, convTokenLimit, onConvTokenLimitChange, chain, app, epCount }: Props) {
  const labelStyle: React.CSSProperties = { fontSize: 12, color: 'var(--tm-card-text-subtle)', marginBottom: 4, display: 'block' };
  const inputStyle: React.CSSProperties = {
    width: '100%', padding: '7px 10px', borderRadius: 6,
    border: `1px solid ${C.outlineVariant}`, background: C.surfaceLow,
    color: 'var(--tm-card-text)', fontSize: 13, boxSizing: 'border-box', outline: 'none',
  };

  return (
    <div style={{ flex: 1, overflowY: 'auto' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '10px 12px', borderRadius: 10, marginBottom: 16, background: 'rgba(0,209,255,0.06)', border: '1px solid rgba(0,209,255,0.18)' }}>
        <span className="material-symbols-outlined" style={{ fontSize: 22, color: C.cyan }}>deployed_code</span>
        <div style={{ minWidth: 0 }}>
          <div style={{ fontWeight: 700, fontSize: 14, color: C.text, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {appName || 'Untitled Application'}
          </div>
          <div style={{ fontSize: 10, color: C.textMuted }}>
            {app ? `ID: ${app.id.slice(0, 8)}…` : 'Not yet saved'}
          </div>
        </div>
      </div>

      <div style={{ marginBottom: 14 }}>
        <label style={labelStyle}>Application Name</label>
        <input
          style={inputStyle}
          value={appName}
          onChange={e => onAppNameChange(e.target.value)}
          placeholder="My Application"
        />
      </div>

      {epCount <= 1 ? (
        <div style={{ marginBottom: 14 }}>
          <label style={labelStyle}>
            Conversation Token Limit
            <span style={{ marginLeft: 6, fontSize: 10, color: '#64748b' }}>per session · blank = unlimited</span>
          </label>
          <input
            type="number" min={1}
            style={inputStyle}
            value={convTokenLimit}
            onChange={e => onConvTokenLimitChange(e.target.value)}
            placeholder="e.g. 50000"
          />
        </div>
      ) : (
        <div style={{ marginBottom: 14, padding: '8px 10px', borderRadius: 6, background: 'rgba(0,240,255,0.05)', border: '1px solid rgba(0,240,255,0.15)', fontSize: 11, color: C.textMuted, lineHeight: 1.5 }}>
          Multiple entry points — select each entry point node to edit its name and token limit individually.
        </div>
      )}

      <div style={{ marginBottom: 14 }}>
        <label style={labelStyle}>Canvas Status</label>
        <div style={{
          display: 'flex', alignItems: 'flex-start', gap: 8,
          padding: '8px 10px', borderRadius: 8,
          background: chain.ready ? 'rgba(74,222,128,0.06)' : 'rgba(255,180,171,0.06)',
          border: `1px solid ${chain.ready ? 'rgba(74,222,128,0.2)' : 'rgba(255,180,171,0.2)'}`,
        }}>
          <span style={{
            width: 7, height: 7, borderRadius: '50%', flexShrink: 0, marginTop: 4,
            background: chain.color, boxShadow: chain.ready ? `0 0 6px ${chain.color}` : 'none',
            display: 'inline-block',
          }} />
          <span style={{ fontSize: 12, color: chain.color, lineHeight: 1.5 }}>{chain.label}</span>
        </div>
      </div>

      <div style={{ marginBottom: 14 }}>
        <label style={labelStyle}>Canvas Info</label>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 6 }}>
          {[
            { label: 'Entry Points', value: String(chain.epNode ? 1 : 0) },
            { label: 'Orchestrator', value: chain.orchNode ? (chain.orchNode.data as OrchestratorData).displayName : '—' },
            { label: 'Agents', value: String(chain.agentCount) },
            { label: 'Status', value: app?.enabled ? 'Deployed' : 'Draft' },
          ].map(({ label, value }) => (
            <div key={label} style={{ padding: '7px 10px', borderRadius: 7, background: C.surfaceLow, border: `1px solid ${C.outlineVariant}` }}>
              <div style={{ fontSize: 10, color: C.textMuted, marginBottom: 2 }}>{label}</div>
              <div style={{ fontSize: 12, color: C.text, fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{value}</div>
            </div>
          ))}
        </div>
      </div>

      {app && (
        <div style={{ marginBottom: 14 }}>
          <label style={labelStyle}>Created</label>
          <div style={{ fontSize: 12, color: C.textMuted }}>
            {new Date(app.created_at).toLocaleString()}
          </div>
        </div>
      )}

      <div style={{ marginTop: 8, padding: '8px 0', borderTop: `1px solid ${C.outlineVariant}`, fontSize: 11, color: C.textMuted, lineHeight: 1.6 }}>
        Click any node to edit its properties.
      </div>
    </div>
  );
}
