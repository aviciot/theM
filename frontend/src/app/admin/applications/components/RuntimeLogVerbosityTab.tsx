'use client';
import { useEffect, useState } from 'react';
import { themApi, type LogVerbosity } from '@/lib/api';
import { C } from '../constants';
import { sharedField, sharedLbl } from './RuntimeShared';

const OPTIONS: Array<{ value: LogVerbosity; label: string; description: string }> = [
  { value: 'off', label: 'Off', description: 'No per-node trace is stored. The live run stream still works while a viewer is connected, but nothing survives after the run ends — the Flow tab in Run History stays empty.' },
  { value: 'status', label: 'Status', description: 'Each node’s id, kind, status, and latency are stored. Output and error detail are never persisted. Cheap enough to leave on for production traffic.' },
  { value: 'full', label: 'Full', description: 'Everything in Status, plus each node’s output or error detail. Debug sessions always use this level regardless of what’s set here.' },
];

export function RuntimeLogVerbosityTab({ appId }: { appId: string }) {
  const [value, setValue] = useState<LogVerbosity>('status');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);

  useEffect(() => {
    themApi.getLogVerbosity(appId)
      .then(cfg => setValue(cfg.log_verbosity))
      .catch(() => setValue('status'))
      .finally(() => setLoading(false));
  }, [appId]);

  async function handleSave(next: LogVerbosity) {
    setSaving(true); setMsg(null);
    try {
      const saved = await themApi.putLogVerbosity(appId, next);
      setValue(saved.log_verbosity);
      setMsg({ ok: true, text: 'Saved' });
    } catch (e: unknown) {
      setMsg({ ok: false, text: e instanceof Error ? e.message : 'Save failed' });
    } finally {
      setSaving(false);
    }
  }

  if (loading) {
    return <div style={{ padding: '32px', textAlign: 'center', color: C.textMuted, fontSize: 13 }}>Loading…</div>;
  }

  return (
    <div style={{ padding: '4px 0' }}>
      <p style={{ fontSize: 13, color: C.textMuted, margin: '0 0 20px 0', lineHeight: 1.5 }}>
        Controls how much of each Graph-mode run&apos;s per-node trace is written to Run History for this application.
      </p>

      <label style={sharedLbl}>Trace Log Verbosity</label>
      <select
        style={sharedField}
        value={value}
        disabled={saving}
        onChange={e => handleSave(e.target.value as LogVerbosity)}
      >
        {OPTIONS.map(o => (
          <option key={o.value} value={o.value}>{o.label}</option>
        ))}
      </select>

      <div style={{ marginTop: 12, display: 'flex', flexDirection: 'column', gap: 8 }}>
        {OPTIONS.map(o => (
          <div key={o.value} style={{
            padding: '10px 12px', borderRadius: 8, fontSize: 12, lineHeight: 1.5,
            color: o.value === value ? C.text : C.textMuted,
            background: o.value === value ? 'rgba(124,58,237,0.08)' : 'rgba(255,255,255,0.02)',
            border: `1px solid ${o.value === value ? 'rgba(124,58,237,0.24)' : 'rgba(255,255,255,0.06)'}`,
          }}>
            <span style={{ fontWeight: 700 }}>{o.label}:</span> {o.description}
          </div>
        ))}
      </div>

      {msg && (
        <div style={{ marginTop: 12, fontSize: 12, color: msg.ok ? '#4ade80' : '#f87171' }}>{msg.text}</div>
      )}
    </div>
  );
}
