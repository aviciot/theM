'use client';
import { use, useState, useEffect } from 'react';
import { AppDetailShell } from '../AppDetailShell';
import { AppPageLoader, AppPageError } from '../AppPageLoader';
import { useAppById } from '../useAppById';
import { C } from '../../constants';
import { themApi } from '@/lib/api';

export default function AppSpecPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  const { app, loading, error } = useAppById(id);
  const [spec, setSpec] = useState('');
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    if (app) setSpec(app.spec ?? '');
  }, [app]);

  if (loading) return <AppPageLoader />;
  if (error || !app) return <AppPageError message={error ?? 'Application not found'} />;

  const handleSave = async () => {
    setSaving(true);
    setSaved(false);
    try {
      await themApi.patchAppSpec(id, spec);
      setSaved(true);
      setTimeout(() => setSaved(false), 2000);
    } finally {
      setSaving(false);
    }
  };

  return (
    <AppDetailShell app={app}>
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', padding: '28px 32px', gap: 16, overflow: 'auto' }}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
          <div>
            <div style={{ fontSize: 15, fontWeight: 700, color: C.text }}>App Spec</div>
            <div style={{ fontSize: 12, color: C.textMuted, marginTop: 3 }}>
              Document what this app does and how it should be built. Markdown supported.
            </div>
          </div>
          <button
            onClick={handleSave}
            disabled={saving}
            style={{
              padding: '8px 20px', borderRadius: 8, border: 'none', cursor: saving ? 'not-allowed' : 'pointer',
              background: saved ? '#4ade80' : C.cyan, color: '#021520',
              fontSize: 13, fontWeight: 700, opacity: saving ? 0.6 : 1,
              transition: 'background 0.2s',
            }}
          >
            {saving ? 'Saving…' : saved ? 'Saved ✓' : 'Save'}
          </button>
        </div>

        <textarea
          value={spec}
          onChange={e => setSpec(e.target.value)}
          placeholder={`# ${app.name}\n\n## What it does\n\n## Flow\n\n## Nodes\n`}
          spellCheck={false}
          style={{
            flex: 1, minHeight: 480,
            background: 'rgba(255,255,255,0.03)', border: `1px solid ${C.outline}`,
            borderRadius: 10, padding: '16px 18px',
            color: C.text, fontSize: 13, fontFamily: 'JetBrains Mono, monospace',
            lineHeight: 1.65, resize: 'vertical', outline: 'none',
          }}
          onFocus={e => { e.target.style.borderColor = C.cyan; }}
          onBlur={e => { e.target.style.borderColor = C.outline; }}
        />
      </div>
    </AppDetailShell>
  );
}
