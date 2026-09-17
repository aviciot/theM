'use client';
import { useEffect, useState } from 'react';
import { themApi, type TemporalConfig } from '@/lib/api';
import { C } from '../constants';
import { sharedField, sharedLbl } from './RuntimeShared';

const PLATFORM_DEFAULTS: TemporalConfig = {
  max_concurrent_workflows: 10,
  workflow_timeout_s: 3600,
  activity_timeout_s: 600,
  retry_max_attempts: 3,
};

type Override = {
  max_concurrent_workflows: string;
  workflow_timeout_s: string;
  activity_timeout_s: string;
  retry_max_attempts: string;
};

function emptyOverride(): Override {
  return { max_concurrent_workflows: '', workflow_timeout_s: '', activity_timeout_s: '', retry_max_attempts: '' };
}

// Parse effective values into the draft override inputs (empty = not overridden).
// Since the API always returns merged effective values, we have no direct way to tell
// which fields are overridden at the DB level from a single fetch. We represent
// overrides as empty (= inherit) or a concrete number in the input.
function initOverride(effective: TemporalConfig, defaults: TemporalConfig): Override {
  return {
    max_concurrent_workflows: effective.max_concurrent_workflows !== defaults.max_concurrent_workflows ? String(effective.max_concurrent_workflows) : '',
    workflow_timeout_s:        effective.workflow_timeout_s        !== defaults.workflow_timeout_s        ? String(effective.workflow_timeout_s)        : '',
    activity_timeout_s:        effective.activity_timeout_s        !== defaults.activity_timeout_s        ? String(effective.activity_timeout_s)        : '',
    retry_max_attempts:        effective.retry_max_attempts        !== defaults.retry_max_attempts        ? String(effective.retry_max_attempts)        : '',
  };
}

const hint: React.CSSProperties = { fontSize: 11, color: C.textMuted, marginTop: 4, lineHeight: 1.4 };

export function RuntimeTemporalTab({ appId }: { appId: string }) {
  const [effective, setEffective] = useState<TemporalConfig | null>(null);
  const [defaults,  setDefaults]  = useState<TemporalConfig>(PLATFORM_DEFAULTS);
  const [override,  setOverride]  = useState<Override>(emptyOverride());
  const [loading,   setLoading]   = useState(true);
  const [saving,    setSaving]    = useState(false);
  const [msg,       setMsg]       = useState<{ ok: boolean; text: string } | null>(null);

  useEffect(() => {
    Promise.all([
      themApi.getTemporalAppConfig(appId),
      themApi.getTemporalPlatformConfig().catch(() => PLATFORM_DEFAULTS),
    ]).then(([eff, defs]) => {
      setEffective(eff);
      setDefaults(defs);
      setOverride(initOverride(eff, defs));
    }).catch(() => {
      setEffective(PLATFORM_DEFAULTS);
    }).finally(() => setLoading(false));
  }, [appId]);

  async function handleSave() {
    setSaving(true); setMsg(null);
    const patch = {
      max_concurrent_workflows: override.max_concurrent_workflows !== '' ? parseInt(override.max_concurrent_workflows) : null,
      workflow_timeout_s:        override.workflow_timeout_s        !== '' ? parseInt(override.workflow_timeout_s)        : null,
      activity_timeout_s:        override.activity_timeout_s        !== '' ? parseInt(override.activity_timeout_s)        : null,
      retry_max_attempts:        override.retry_max_attempts        !== '' ? parseInt(override.retry_max_attempts)        : null,
    };
    try {
      const saved = await themApi.putTemporalAppConfig(appId, patch);
      setEffective(saved);
      setOverride(initOverride(saved, defaults));
      setMsg({ ok: true, text: 'Saved' });
    } catch (e: unknown) {
      setMsg({ ok: false, text: e instanceof Error ? e.message : 'Save failed' });
    } finally {
      setSaving(false);
    }
  }

  const f = sharedField;
  const l = sharedLbl;

  if (loading) {
    return <div style={{ padding: '32px', textAlign: 'center', color: C.textMuted, fontSize: 13 }}>Loading…</div>;
  }

  const fields: Array<{ key: keyof Override; label: string; defaultVal: number; description: string; hintText: string }> = [
    { key: 'max_concurrent_workflows', label: 'Max Concurrent Workflows', defaultVal: defaults.max_concurrent_workflows, description: 'Platform default', hintText: 'Empty = inherit platform default.' },
    { key: 'retry_max_attempts',       label: 'Retry Max Attempts',       defaultVal: defaults.retry_max_attempts,       description: 'Platform default', hintText: 'Empty = inherit platform default.' },
    { key: 'workflow_timeout_s',       label: 'Workflow Timeout (s)',      defaultVal: defaults.workflow_timeout_s,       description: 'Platform default', hintText: 'Max wall-clock time per run. Empty = inherit.' },
    { key: 'activity_timeout_s',       label: 'Activity Timeout (s)',      defaultVal: defaults.activity_timeout_s,       description: 'Platform default', hintText: 'Schedule-to-close per activity. Empty = inherit.' },
  ];

  return (
    <div style={{ padding: '4px 0' }}>
      <p style={{ fontSize: 13, color: C.textMuted, margin: '0 0 20px 0', lineHeight: 1.5 }}>
        Override Temporal execution parameters for this application. Leave a field empty to inherit the platform default.
      </p>

      {/* Effective values row */}
      {effective && (
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginBottom: 24 }}>
          {([
            { label: 'Concurrent', value: effective.max_concurrent_workflows },
            { label: 'WF Timeout', value: `${effective.workflow_timeout_s}s` },
            { label: 'Act Timeout', value: `${effective.activity_timeout_s}s` },
            { label: 'Max Retries', value: effective.retry_max_attempts },
          ] as const).map(({ label, value }) => (
            <div key={label} style={{ flex: '1 0 100px', padding: '8px 12px', borderRadius: 8, background: 'rgba(124,58,237,0.06)', border: '1px solid rgba(124,58,237,0.18)', textAlign: 'center' }}>
              <div style={{ fontSize: 16, fontWeight: 700, color: '#a78bfa' }}>{value}</div>
              <div style={{ fontSize: 10, color: C.textMuted, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.06em' }}>{label}</div>
            </div>
          ))}
        </div>
      )}

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16, marginBottom: 20 }}>
        {fields.map(({ key, label, defaultVal, hintText }) => (
          <div key={key}>
            <label style={l}>{label}</label>
            <input
              type="number" min={0} style={f}
              placeholder={`${defaultVal} (platform default)`}
              value={override[key]}
              onChange={e => { setOverride(o => ({ ...o, [key]: e.target.value })); setMsg(null); }}
            />
            <div style={hint}>{hintText}</div>
          </div>
        ))}
      </div>

      <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
        <button
          onClick={handleSave} disabled={saving}
          style={{ padding: '8px 20px', borderRadius: 7, border: '1px solid rgba(167,139,250,0.4)', background: 'rgba(208,188,255,0.07)', color: '#a78bfa', cursor: saving ? 'not-allowed' : 'pointer', fontSize: 13, fontWeight: 600, opacity: saving ? 0.45 : 1 }}
        >
          {saving ? '…' : 'Save Overrides'}
        </button>
        {msg && (
          <span style={{ fontSize: 12, color: msg.ok ? '#4ade80' : '#f87171' }}>{msg.text}</span>
        )}
      </div>

      <div style={{ marginTop: 20, padding: '12px 16px', borderRadius: 8, background: 'rgba(255,255,255,0.02)', border: '1px solid rgba(255,255,255,0.06)', fontSize: 12, color: C.textMuted, lineHeight: 1.6 }}>
        <span className="material-symbols-outlined" style={{ fontSize: 13, verticalAlign: 'middle', marginRight: 5 }}>info</span>
        Set a field to null (empty) to clear an override and revert to the platform default. Effective values shown above reflect the merged result.
      </div>
    </div>
  );
}
