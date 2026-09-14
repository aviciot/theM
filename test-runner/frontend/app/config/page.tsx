'use client';
import { useEffect, useState } from 'react';
import { api } from '@/lib/api';

export default function ConfigPage() {
  const [themURL, setThemURL] = useState('');
  const [adminUser, setAdminUser] = useState('');
  const [adminPass, setAdminPass] = useState('');
  const [passSet, setPassSet] = useState(false);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<{ ok: boolean; error?: string; tenants_found?: number } | null>(null);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    api.getConfig().then((c) => {
      setThemURL(c.them_url);
      setAdminUser(c.admin_user);
      setPassSet(c.admin_pass_set);
    });
  }, []);

  const save = async () => {
    setSaving(true);
    setSaved(false);
    try {
      await api.putConfig(themURL, adminUser, adminPass);
      setSaved(true);
      setAdminPass('');
      setPassSet(adminPass !== '' || passSet);
    } finally {
      setSaving(false);
    }
  };

  const test = async () => {
    setTesting(true);
    setTestResult(null);
    try {
      const r = await api.testConfig();
      setTestResult(r);
    } catch (e: unknown) {
      setTestResult({ ok: false, error: e instanceof Error ? e.message : 'unknown error' });
    } finally {
      setTesting(false);
    }
  };

  return (
    <div className="p-8 max-w-xl">
      <h1 className="text-2xl font-semibold mb-1">Configuration</h1>
      <p className="text-gray-400 text-sm mb-8">Connect the test runner to your the-M instance.</p>

      <div className="space-y-5">
        <Field label="the-M Base URL" hint="e.g. http://10.55.125.43:8088">
          <input
            className="input"
            value={themURL}
            onChange={(e) => setThemURL(e.target.value)}
            placeholder="http://..."
          />
        </Field>

        <Field label="Admin Username">
          <input
            className="input"
            value={adminUser}
            onChange={(e) => setAdminUser(e.target.value)}
          />
        </Field>

        <Field label="Admin Password" hint={passSet ? 'Password saved — leave blank to keep current' : ''}>
          <input
            className="input"
            type="password"
            value={adminPass}
            onChange={(e) => setAdminPass(e.target.value)}
            placeholder={passSet ? '••••••••' : 'Enter password'}
          />
        </Field>

        <div className="flex gap-3 pt-2">
          <button className="btn-primary" onClick={save} disabled={saving}>
            {saving ? 'Saving…' : 'Save'}
          </button>
          <button className="btn-secondary" onClick={test} disabled={testing}>
            {testing ? 'Testing…' : 'Test Connection'}
          </button>
        </div>

        {saved && <p className="text-green-400 text-sm">✓ Configuration saved.</p>}

        {testResult && (
          <div className={`rounded-lg p-4 text-sm ${testResult.ok ? 'bg-green-900/30 border border-green-700' : 'bg-red-900/30 border border-red-700'}`}>
            {testResult.ok ? (
              <span className="text-green-300">✓ Connected — {testResult.tenants_found} tenants found</span>
            ) : (
              <span className="text-red-300">✗ {testResult.error}</span>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div>
      <label className="block text-sm font-medium text-gray-300 mb-1">{label}</label>
      {children}
      {hint && <p className="text-xs text-gray-500 mt-1">{hint}</p>}
    </div>
  );
}
