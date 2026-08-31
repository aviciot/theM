'use client';
import type { Node } from '@xyflow/react';
import type { MiddlewareData } from '../../types';
import { C } from '../../constants';
import { labelStyle, inputStyle, fieldWrap } from './panelStyles';

interface Props {
  selectedNode: Node;
  onUpdateNode: (id: string, data: Record<string, unknown>) => void;
}

export function MiddlewarePanel({ selectedNode, onUpdateNode }: Props) {
  const d = selectedNode.data as MiddlewareData;
  const icon = d.kind === 'guard' ? 'shield' : 'bolt';
  const co = (d.configOverride ?? {}) as Record<string, unknown>;

  function setOverride(patch: Record<string, unknown>) {
    onUpdateNode(selectedNode.id, { configOverride: { ...co, ...patch } });
  }

  const kindBadge = (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, padding: '3px 10px', borderRadius: 20, fontSize: 11, fontWeight: 600, background: C.amberBg, color: C.amber, border: `1px solid ${C.amberBorder}` }}>
      <span style={{ width: 5, height: 5, borderRadius: '50%', background: C.amber, boxShadow: `0 0 5px ${C.amber}` }} />
      {d.kind}
    </span>
  );

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '10px 12px', borderRadius: 10, marginBottom: 16, background: C.amberBg, border: `1px solid ${C.amberBorder}` }}>
        <span className="material-symbols-outlined" style={{ fontSize: 22, color: C.amber }}>{icon}</span>
        <div style={{ minWidth: 0 }}>
          <div style={{ fontWeight: 700, fontSize: 14, color: C.text, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{d.displayName}</div>
          <div style={{ fontSize: 11, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace' }}>{d.slug}</div>
        </div>
      </div>
      <div style={fieldWrap}>
        <label style={labelStyle}>Kind</label>
        {kindBadge}
      </div>
      {d.description && (
        <div style={fieldWrap}>
          <label style={labelStyle}>Description</label>
          <div style={{ fontSize: 12, color: 'var(--tm-card-text-hint)', lineHeight: 1.55, padding: '7px 10px', borderRadius: 6, border: `1px solid ${C.outlineVariant}`, background: C.surfaceLow }}>
            {d.description}
          </div>
        </div>
      )}
      <div style={{ marginTop: 8, marginBottom: 8, paddingTop: 8, borderTop: `1px solid ${C.outlineVariant}`, fontSize: 11, fontWeight: 700, color: C.textMuted, letterSpacing: 1, textTransform: 'uppercase' }}>
        Config Override
      </div>
      {d.kind === 'guard' && (
        <>
          <div style={fieldWrap}>
            <label style={labelStyle}>Mode</label>
            <select
              style={{ ...inputStyle }}
              value={(co.mode as string) ?? ''}
              onChange={e => setOverride({ mode: e.target.value || undefined })}
            >
              <option value="">— default —</option>
              <option value="block">block</option>
              <option value="redact">redact</option>
            </select>
          </div>
          <div style={{ ...fieldWrap, display: 'flex', flexDirection: 'column', gap: 8 }}>
            <label style={{ ...labelStyle, marginBottom: 0 }}>Detection</label>
            <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: C.text, cursor: 'pointer' }}>
              <input
                type="checkbox"
                checked={co.pii_detection !== false}
                onChange={e => setOverride({ pii_detection: e.target.checked })}
                style={{ accentColor: C.amber }}
              />
              PII detection
            </label>
            <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: C.text, cursor: 'pointer' }}>
              <input
                type="checkbox"
                checked={co.injection_detection !== false}
                onChange={e => setOverride({ injection_detection: e.target.checked })}
                style={{ accentColor: C.amber }}
              />
              Injection detection
            </label>
          </div>
        </>
      )}
      {d.kind === 'cache' && (
        <>
          <div style={fieldWrap}>
            <label style={labelStyle}>TTL (seconds)</label>
            <input
              type="number"
              min={1}
              style={inputStyle}
              value={(co.ttl_seconds as number | undefined) ?? ''}
              placeholder="e.g. 300"
              onChange={e => setOverride({ ttl_seconds: e.target.value ? parseInt(e.target.value, 10) : undefined })}
            />
          </div>
          <div style={fieldWrap}>
            <label style={labelStyle}>Scope</label>
            <select
              style={{ ...inputStyle }}
              value={(co.scope as string) ?? ''}
              onChange={e => setOverride({ scope: e.target.value || undefined })}
            >
              <option value="">— default —</option>
              <option value="global">global</option>
              <option value="app">app</option>
              <option value="session">session</option>
              <option value="user">user</option>
            </select>
          </div>
          <div style={fieldWrap}>
            <label style={labelStyle}>Max result chars</label>
            <input
              type="number"
              min={1}
              style={inputStyle}
              value={(co.max_result_chars as number | undefined) ?? ''}
              placeholder="e.g. 8000"
              onChange={e => setOverride({ max_result_chars: e.target.value ? parseInt(e.target.value, 10) : undefined })}
            />
          </div>
        </>
      )}
    </div>
  );
}
