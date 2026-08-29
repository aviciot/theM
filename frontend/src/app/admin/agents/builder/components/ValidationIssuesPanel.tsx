import React from 'react';
import type { AgentIssue } from '@/lib/api';

interface ValidationIssuesPanelProps {
  issues: AgentIssue[];
  errorCount: number;
  show: boolean;
}

export function ValidationIssuesPanel({ issues, errorCount, show }: ValidationIssuesPanelProps) {
  if (!show) return null;
  return (
    <div style={{
      flexShrink: 0, maxHeight: '130px', overflowY: 'auto',
      borderBottom: `1px solid ${errorCount > 0 ? 'rgba(248,113,113,0.3)' : 'rgba(245,158,11,0.3)'}`,
      background: errorCount > 0 ? 'rgba(248,113,113,0.04)' : 'rgba(245,158,11,0.04)',
      padding: '6px 16px',
    }}>
      {issues.map((iss, i) => (
        <div key={i} style={{ display: 'flex', alignItems: 'flex-start', gap: '8px', padding: '3px 0', borderBottom: i < issues.length - 1 ? '1px solid rgba(255,255,255,0.04)' : 'none' }}>
          <span style={{ fontSize: '11px', color: iss.severity === 'error' ? '#f87171' : '#f59e0b', flexShrink: 0, marginTop: '1px' }}>
            {iss.severity === 'error' ? '✗' : '⚠'}
          </span>
          <span style={{ fontSize: '11px', color: '#e2e8f0', flex: 1 }}>
            <span style={{ fontFamily: 'monospace', color: '#94a3b8', marginRight: '6px' }}>[{iss.code}]</span>
            {iss.message}
            {iss.field && <span style={{ marginLeft: '6px', color: '#64748b' }}>· field: <code style={{ color: '#f59e0b' }}>{iss.field}</code></span>}
          </span>
        </div>
      ))}
    </div>
  );
}
