'use client';
import { useEffect, useState } from 'react';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { themApi, type TemporalConfig } from '@/lib/api';

const DEFAULTS: TemporalConfig = {
  max_concurrent_workflows: 10,
  workflow_timeout_s: 3600,
  activity_timeout_s: 600,
  retry_max_attempts: 3,
};

const field: React.CSSProperties = {
  width: '100%', padding: '9px 12px', borderRadius: 7,
  border: '1px solid rgba(255,255,255,0.1)', background: 'rgba(255,255,255,0.05)',
  color: '#e8eaed', fontSize: 13, outline: 'none', boxSizing: 'border-box',
};
const lbl: React.CSSProperties = {
  fontSize: 11, fontWeight: 600, color: 'rgba(255,255,255,0.45)', letterSpacing: '0.06em',
  textTransform: 'uppercase', marginBottom: 5, display: 'block',
};
const hint: React.CSSProperties = { fontSize: 11, color: 'rgba(255,255,255,0.3)', marginTop: 4 };

export default function TemporalPlatformPage() {
  const [cfg, setCfg]         = useState<TemporalConfig>(DEFAULTS);
  const [loading, setLoading] = useState(true);
  const [saving,  setSaving]  = useState(false);
  const [msg,     setMsg]     = useState<{ ok: boolean; text: string } | null>(null);

  useEffect(() => {
    themApi.getTemporalPlatformConfig()
      .then(c => setCfg(c))
      .catch(() => {})
      .finally(() => setLoading(false));
  }, []);

  async function handleSave() {
    setSaving(true); setMsg(null);
    try {
      const saved = await themApi.putTemporalPlatformConfig(cfg);
      setCfg(saved);
      setMsg({ ok: true, text: 'Saved' });
    } catch (e: unknown) {
      setMsg({ ok: false, text: e instanceof Error ? e.message : 'Save failed' });
    } finally {
      setSaving(false);
    }
  }

  function num(val: number) { return isNaN(val) ? 0 : val; }

  return (
    <AuthGuard>
      <div style={{ display: 'flex', minHeight: '100vh', background: 'var(--tm-bg)' }}>
        <Sidebar />
        <main style={{ marginLeft: '260px', flex: 1, background: 'var(--tm-bg)' }}>
          <div style={{ padding: '40px 32px 0' }}>
            <h2 style={{ fontSize: '40px', fontWeight: 800, color: '#fff', margin: '0 0 6px 0', letterSpacing: '-0.03em', lineHeight: 1.1 }}>
              Temporal
            </h2>
            <p style={{ fontSize: '14px', color: 'var(--tm-card-text-muted)', margin: '0 0 32px 0' }}>
              Platform-wide defaults for Temporal workflow execution. Per-app overrides are configured in each application&apos;s Runtime → Temporal tab.
            </p>
          </div>

          <div style={{ padding: '0 32px 64px', maxWidth: '640px' }}>
            {loading && (
              <div style={{ padding: '60px', textAlign: 'center', color: 'var(--tm-card-text-muted)', fontSize: '14px' }}>
                Loading…
              </div>
            )}

            {!loading && (
              <div style={{ background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(255,255,255,0.08)', borderRadius: 12, padding: '28px 28px 24px' }}>
                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 20, marginBottom: 20 }}>
                  <div>
                    <label style={lbl}>Max Concurrent Workflows</label>
                    <input
                      type="number" min={1} style={field}
                      value={cfg.max_concurrent_workflows}
                      onChange={e => setCfg(c => ({ ...c, max_concurrent_workflows: num(parseInt(e.target.value)) }))}
                    />
                    <div style={hint}>Platform-wide cap across all apps.</div>
                  </div>
                  <div>
                    <label style={lbl}>Retry Max Attempts</label>
                    <input
                      type="number" min={0} style={field}
                      value={cfg.retry_max_attempts}
                      onChange={e => setCfg(c => ({ ...c, retry_max_attempts: num(parseInt(e.target.value)) }))}
                    />
                    <div style={hint}>Activity retry limit before marking as failed.</div>
                  </div>
                  <div>
                    <label style={lbl}>Workflow Timeout (seconds)</label>
                    <input
                      type="number" min={1} style={field}
                      value={cfg.workflow_timeout_s}
                      onChange={e => setCfg(c => ({ ...c, workflow_timeout_s: num(parseInt(e.target.value)) }))}
                    />
                    <div style={hint}>Max wall-clock time per workflow run.</div>
                  </div>
                  <div>
                    <label style={lbl}>Activity Timeout (seconds)</label>
                    <input
                      type="number" min={1} style={field}
                      value={cfg.activity_timeout_s}
                      onChange={e => setCfg(c => ({ ...c, activity_timeout_s: num(parseInt(e.target.value)) }))}
                    />
                    <div style={hint}>Schedule-to-close timeout per activity.</div>
                  </div>
                </div>

                <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                  <button
                    onClick={handleSave} disabled={saving}
                    style={{ padding: '10px 24px', borderRadius: 8, border: 'none', cursor: saving ? 'not-allowed' : 'pointer', background: 'var(--tm-accent, #7c3aed)', color: '#fff', fontSize: 14, fontWeight: 700, opacity: saving ? 0.6 : 1 }}
                  >
                    {saving ? 'Saving…' : 'Save'}
                  </button>
                  {msg && (
                    <span style={{ fontSize: 13, color: msg.ok ? '#4ade80' : '#f87171' }}>{msg.text}</span>
                  )}
                </div>
              </div>
            )}

            <div style={{ marginTop: 24, padding: '14px 18px', borderRadius: 10, background: 'rgba(124,58,237,0.06)', border: '1px solid rgba(124,58,237,0.18)' }}>
              <div style={{ display: 'flex', alignItems: 'flex-start', gap: 10 }}>
                <span className="material-symbols-outlined" style={{ fontSize: 16, color: 'rgba(167,139,250,0.8)', flexShrink: 0, marginTop: 1 }}>info</span>
                <div style={{ fontSize: 12, color: 'rgba(255,255,255,0.45)', lineHeight: 1.6 }}>
                  These values are platform defaults. Individual applications can override any field — the effective value is the per-app override when set, falling back to these defaults.
                </div>
              </div>
            </div>
          </div>
        </main>
      </div>
    </AuthGuard>
  );
}
