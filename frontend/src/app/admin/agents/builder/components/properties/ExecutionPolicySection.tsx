import { useState } from 'react';
import { getNodeDef } from '@/lib/nodeRegistry';
import type { StepPolicyOverride } from '../../types';
import { C, labelStyle, inputStyle, fieldGap, hint } from '../../constants';

interface ExecutionPolicySectionProps {
  policy: StepPolicyOverride;
  defaultPolicy: NonNullable<ReturnType<typeof getNodeDef>['default_policy']>;
  maxPolicy?: ReturnType<typeof getNodeDef>['max_policy'];
  setPolicyField: (key: keyof StepPolicyOverride, value: number | undefined) => void;
}

export function ExecutionPolicySection({ policy, defaultPolicy, maxPolicy, setPolicyField }: ExecutionPolicySectionProps) {
  const [open, setOpen] = useState(false);

  const hasOverride = !!(policy.max_attempts || policy.timeout_seconds ||
    policy.initial_interval_seconds || policy.backoff_coefficient || policy.max_interval_seconds);

  const ACCENT = '#7c3aed';

  return (
    <div style={{ marginTop: 16, borderTop: `1px solid ${C.outline}`, paddingTop: 12 }}>
      <button
        onClick={() => setOpen(o => !o)}
        style={{
          background: 'transparent', border: 'none', cursor: 'pointer', width: '100%',
          display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: 0,
        }}
      >
        <span style={{ color: ACCENT, fontSize: '10px', fontWeight: 700, letterSpacing: '0.08em' }}>
          EXECUTION POLICY{hasOverride ? ' ✎' : ''}
        </span>
        <span style={{ color: C.textMuted, fontSize: 11 }}>{open ? '▲' : '▼'}</span>
      </button>

      {open && (
        <div style={{ marginTop: 10 }}>
          <div style={{ fontSize: 11, color: '#64748b', marginBottom: 8, lineHeight: 1.5 }}>
            Leave blank to use the node default. Values are clamped to the node&apos;s maximum.
          </div>

          <PolicyNumField
            label="Max Attempts"
            hint={`default ${defaultPolicy.max_attempts}${maxPolicy?.max_attempts ? ` · max ${maxPolicy.max_attempts}` : ''}`}
            value={policy.max_attempts}
            min={1}
            max={maxPolicy?.max_attempts ?? 10}
            onChange={v => setPolicyField('max_attempts', v)}
          />
          <PolicyNumField
            label="Timeout (seconds)"
            hint={`default ${defaultPolicy.timeout_seconds}${maxPolicy?.timeout_seconds ? ` · max ${maxPolicy.timeout_seconds}` : ''}`}
            value={policy.timeout_seconds}
            min={1}
            max={maxPolicy?.timeout_seconds ?? 3600}
            onChange={v => setPolicyField('timeout_seconds', v)}
          />
          {(defaultPolicy.max_attempts ?? 1) > 1 || (policy.max_attempts ?? 0) > 1 ? (
            <>
              <PolicyNumField
                label="Initial Interval (s)"
                hint={`default ${defaultPolicy.initial_interval_seconds ?? 1}`}
                value={policy.initial_interval_seconds}
                min={0.1}
                max={60}
                step={0.1}
                onChange={v => setPolicyField('initial_interval_seconds', v)}
              />
              <PolicyNumField
                label="Backoff Coefficient"
                hint={`default ${defaultPolicy.backoff_coefficient ?? 2.0}`}
                value={policy.backoff_coefficient}
                min={1.0}
                max={10}
                step={0.1}
                onChange={v => setPolicyField('backoff_coefficient', v)}
              />
              <PolicyNumField
                label="Max Interval (s)"
                hint={`default ${defaultPolicy.max_interval_seconds ?? 30}`}
                value={policy.max_interval_seconds}
                min={1}
                max={maxPolicy?.max_interval_seconds ?? 600}
                onChange={v => setPolicyField('max_interval_seconds', v)}
              />
            </>
          ) : null}

          {hasOverride && (
            <button
              onClick={() => {
                setPolicyField('max_attempts', undefined);
                setPolicyField('timeout_seconds', undefined);
                setPolicyField('initial_interval_seconds', undefined);
                setPolicyField('backoff_coefficient', undefined);
                setPolicyField('max_interval_seconds', undefined);
              }}
              style={{ marginTop: 8, fontSize: 10, color: '#94a3b8', background: 'transparent', border: 'none', cursor: 'pointer', padding: 0 }}
            >
              Reset to defaults
            </button>
          )}
        </div>
      )}
    </div>
  );
}

interface PolicyNumFieldProps {
  label: string;
  hint: string;
  value: number | undefined;
  min: number;
  max: number;
  step?: number;
  onChange: (v: number | undefined) => void;
}

function PolicyNumField({ label, hint: hintText, value, min, max, step = 1, onChange }: PolicyNumFieldProps) {
  return (
    <div style={{ ...fieldGap, marginBottom: 6 }}>
      <label style={labelStyle}>{label} <span style={hint}>{hintText}</span></label>
      <input
        type="number"
        min={min}
        max={max}
        step={step}
        value={value ?? ''}
        placeholder="default"
        onChange={e => {
          const v = e.target.value === '' ? undefined : parseFloat(e.target.value);
          onChange(v && !isNaN(v) ? v : undefined);
        }}
        style={{ ...inputStyle, width: '100%' }}
      />
    </div>
  );
}
