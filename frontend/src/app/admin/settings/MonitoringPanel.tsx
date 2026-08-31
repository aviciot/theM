'use client';
import type { MonitoringConfig } from '@/lib/api';
import { MONITORING_DEFAULTS } from './settingsConstants';

function SliderField({
  label, hint, value, min, max, step, onChange, unit, color,
}: {
  label: string; hint?: string; value: number; min: number; max: number;
  step: number; onChange: (v: number) => void; unit?: string; color?: string;
}) {
  const pct = Math.round(((value - min) / (max - min)) * 100);
  const accent = color ?? 'var(--tm-accent)';
  return (
    <div style={{ marginBottom: '20px' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', marginBottom: '8px' }}>
        <label style={{ fontSize: '11px', fontWeight: 700, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.06em' }}>{label}</label>
        <span style={{ fontSize: '15px', fontWeight: 700, color: accent, fontFamily: 'JetBrains Mono, monospace', minWidth: '48px', textAlign: 'right' }}>{value}{unit}</span>
      </div>
      <input type="range" min={min} max={max} step={step} value={value} onChange={(e) => onChange(Number(e.target.value))} style={{ width: '100%', accentColor: accent, cursor: 'pointer', height: '4px' }} />
      <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: '3px' }}>
        <span style={{ fontSize: '10px', color: 'var(--tm-text-muted)', opacity: 0.5 }}>{min}{unit}</span>
        <span style={{ fontSize: '10px', color: 'var(--tm-text-muted)', opacity: 0.5 }}>{max}{unit}</span>
      </div>
      {hint && <p style={{ fontSize: '11px', color: 'var(--tm-text-muted)', marginTop: '4px', opacity: 0.75 }}>{hint}</p>}
      <div style={{ height: '2px', background: 'rgba(148,163,184,0.1)', borderRadius: 2, marginTop: '4px', overflow: 'hidden' }}>
        <div style={{ height: '100%', width: `${pct}%`, background: accent, transition: 'width 0.1s', borderRadius: 2 }} />
      </div>
    </div>
  );
}

function SectionHeader({ icon, color, title, subtitle }: { icon: string; color: string; title: string; subtitle: string }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 20 }}>
      <div style={{ width: 36, height: 36, borderRadius: 10, flexShrink: 0, background: `radial-gradient(circle at 30% 25%, ${color}29, transparent 65%), linear-gradient(145deg, rgba(20,32,52,0.96), rgba(8,16,30,0.96))`, border: `1px solid ${color}59`, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
        <span className="material-symbols-outlined" style={{ fontSize: 18, color }}>{icon}</span>
      </div>
      <div>
        <div style={{ fontSize: 15, fontWeight: 700, color: 'var(--tm-text)', letterSpacing: '-0.01em' }}>{title}</div>
        <div style={{ fontSize: 12, color: 'var(--tm-text-muted)' }}>{subtitle}</div>
      </div>
    </div>
  );
}

const divider = <div style={{ height: 1, background: 'rgba(132,157,188,.1)', margin: '8px 0 22px' }} />;

export function MonitoringPanel({
  monConfig,
  setMonConfig,
  monSaving,
  monSaveMsg,
  onSave,
}: {
  monConfig: MonitoringConfig;
  setMonConfig: React.Dispatch<React.SetStateAction<MonitoringConfig>>;
  monSaving: boolean;
  monSaveMsg: { ok: boolean; text: string } | null;
  onSave: () => void;
}) {
  return (
    <>
      <p style={{ fontSize: '13px', color: 'var(--tm-text-muted)', margin: '0 0 24px 0', lineHeight: 1.5 }}>
        Control how the live Sessions heatmap responds to traffic. Changes take effect immediately in the Sessions view.
      </p>

      <div style={{
        background: 'linear-gradient(160deg, rgba(255,255,255,0.028) 0%, rgba(255,255,255,0.006) 40%, rgba(0,0,0,0.06) 100%), var(--tm-card)',
        border: '1px solid var(--tm-card-border)', borderRadius: '18px', padding: '28px 32px',
        backdropFilter: 'blur(12px)',
        boxShadow: '0 8px 32px rgba(0,0,0,0.4), 0 2px 8px rgba(0,0,0,0.25), inset 0 1px 0 rgba(255,255,255,0.04)',
        display: 'flex', flexDirection: 'column', gap: '0',
      }}>
        <SectionHeader icon="heat_map" color="#00d1ff" title="Node Heatmap" subtitle="Sessions per node to trigger each glow intensity" />
        <div style={{ height: 1, background: 'rgba(132,157,188,.1)', marginBottom: 22 }} />

        <SliderField label="Low threshold" hint="Soft glow starts at this many active sessions on a node" value={monConfig.heatmap_low} min={1} max={50} step={1} onChange={(v) => setMonConfig(c => ({ ...c, heatmap_low: Math.min(v, c.heatmap_medium - 1) }))} unit=" sessions" color="#4ade80" />
        <SliderField label="Medium threshold" hint="Medium intensity glow" value={monConfig.heatmap_medium} min={2} max={200} step={1} onChange={(v) => setMonConfig(c => ({ ...c, heatmap_medium: Math.max(v, c.heatmap_low + 1) }))} unit=" sessions" color="#f59e0b" />
        <SliderField label="High threshold" hint="Full bright + strong glow — maximum intensity" value={monConfig.heatmap_high} min={3} max={500} step={1} onChange={(v) => setMonConfig(c => ({ ...c, heatmap_high: Math.max(v, c.heatmap_medium + 1) }))} unit=" sessions" color="#f87171" />

        {divider}

        <SectionHeader icon="linear_scale" color="#d0bcff" title="Edge Thickness" subtitle="Sessions per edge path to scale stroke width" />
        <SliderField label="Thin edge threshold" hint="1.5px stroke when sessions reach this count" value={monConfig.edge_thin} min={1} max={50} step={1} onChange={(v) => setMonConfig(c => ({ ...c, edge_thin: Math.min(v, c.edge_medium - 1) }))} unit=" sessions" color="#4ade80" />
        <SliderField label="Medium edge threshold" hint="3px stroke" value={monConfig.edge_medium} min={2} max={200} step={1} onChange={(v) => setMonConfig(c => ({ ...c, edge_medium: Math.max(v, c.edge_thin + 1) }))} unit=" sessions" color="#f59e0b" />
        <SliderField label="Thick edge threshold" hint="5px stroke — maximum edge width" value={monConfig.edge_thick} min={3} max={500} step={1} onChange={(v) => setMonConfig(c => ({ ...c, edge_thick: Math.max(v, c.edge_medium + 1) }))} unit=" sessions" color="#f87171" />

        {divider}

        <SectionHeader icon="list_alt" color="#f59e0b" title="Display Limits" subtitle="Caps for UI performance under high load" />
        <SliderField label="Max sessions in panel" hint="Sessions beyond this cap are not shown in the right panel list (heatmap still reflects all)" value={monConfig.panel_max_sessions} min={10} max={500} step={10} onChange={(v) => setMonConfig(c => ({ ...c, panel_max_sessions: v }))} unit=" sessions" color="#00d1ff" />
        <SliderField label="Stats window" hint="Rolling time window used for throughput stats" value={monConfig.stats_window_seconds} min={60} max={3600} step={60} onChange={(v) => setMonConfig(c => ({ ...c, stats_window_seconds: v }))} unit="s" color="#00d1ff" />

        <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginTop: 8 }}>
          <div style={{ flex: 1 }} />
          {monSaveMsg && <span style={{ fontSize: 13, fontWeight: 600, color: monSaveMsg.ok ? '#4edea3' : '#f87171' }}>{monSaveMsg.text}</span>}
          <button onClick={() => setMonConfig(MONITORING_DEFAULTS)} style={{ padding: '8px 16px', borderRadius: 8, border: '1px solid var(--tm-border)', background: 'transparent', color: 'var(--tm-text-muted)', cursor: 'pointer', fontSize: 13, fontWeight: 500 }}>
            Reset defaults
          </button>
          <button onClick={onSave} disabled={monSaving} style={{ padding: '8px 22px', borderRadius: 9, border: 'none', background: monSaving ? 'rgba(99,102,241,.5)' : 'var(--tm-accent)', color: '#fff', cursor: monSaving ? 'not-allowed' : 'pointer', fontSize: 14, fontWeight: 600, opacity: monSaving ? 0.7 : 1 }}>
            {monSaving ? 'Saving…' : 'Save'}
          </button>
        </div>
      </div>
    </>
  );
}
