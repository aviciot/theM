'use client';
import { useEffect, useState } from 'react';
import { useParams, useRouter } from 'next/navigation';
import { api } from '@/lib/api';
import type { App, EP, Scenario, Tenant } from '@/lib/types';

const AUTH_MODES = [
  { value: 'token', label: 'Bearer Token', desc: 'the-M managed opaque token (one per virtual user)' },
  { value: 'public', label: 'Public', desc: 'No auth required' },
  { value: 'user_jwt', label: 'Operative JWT', desc: 'the-M user JWT (internal users only)' },
  { value: 'external_jwt', label: 'SSO / External JWT', desc: 'Tenant Keycloak JWT' },
];

export default function ScenarioEditorPage() {
  const { id } = useParams<{ id: string }>();
  const isNew = id === 'new';
  const router = useRouter();

  const [name, setName] = useState('');
  const [tenantSlug, setTenantSlug] = useState('');
  const [appID, setAppID] = useState('');
  const [appSlug, setAppSlug] = useState('');
  const [epSlug, setEPSlug] = useState('');
  const [authMode, setAuthMode] = useState<Scenario['auth_mode']>('token');
  const [nUsers, setNUsers] = useState(1);
  const [messages, setMessages] = useState<string[]>(['Hello!']);

  const [tenants, setTenants] = useState<Tenant[]>([]);
  const [apps, setApps] = useState<App[]>([]);
  const [eps, setEPs] = useState<EP[]>([]);

  const [saving, setSaving] = useState(false);
  const [running, setRunning] = useState(false);
  const [error, setError] = useState('');

  // Load tenants on mount.
  useEffect(() => {
    api.listTenants().then(setTenants).catch(() => setError('Cannot reach the-M — check Config.'));
  }, []);

  // Load existing scenario.
  useEffect(() => {
    if (isNew) return;
    api.listScenarios().then((list) => {
      const sc = list.find((s) => s.id === id);
      if (!sc) return;
      setName(sc.name);
      setTenantSlug(sc.tenant_slug);
      setAppID(sc.app_id);
      setAppSlug(sc.app_slug);
      setEPSlug(sc.ep_slug);
      setAuthMode(sc.auth_mode);
      setNUsers(sc.n_users);
      setMessages(sc.messages);
    });
  }, [id, isNew]);

  // Load apps when tenant changes.
  useEffect(() => {
    if (!tenantSlug) { setApps([]); setAppID(''); return; }
    api.listApps(tenantSlug).then(setApps);
  }, [tenantSlug]);

  // Load EPs when app changes.
  useEffect(() => {
    if (!appID) { setEPs([]); setEPSlug(''); return; }
    api.listEPs(appID).then(setEPs);
  }, [appID]);

  const save = async () => {
    setSaving(true);
    setError('');
    const sc = { name, tenant_slug: tenantSlug, app_id: appID, app_slug: appSlug, ep_slug: epSlug, auth_mode: authMode, n_users: nUsers, messages };
    try {
      if (isNew) {
        await api.createScenario(sc);
      } else {
        await api.updateScenario(id, sc);
      }
      router.push('/scenarios');
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Save failed');
    } finally {
      setSaving(false);
    }
  };

  const run = async () => {
    const sc = { name, tenant_slug: tenantSlug, app_id: appID, app_slug: appSlug, ep_slug: epSlug, auth_mode: authMode, n_users: nUsers, messages };
    setSaving(true);
    setError('');
    try {
      let scenarioId = id;
      if (isNew) {
        const created = await api.createScenario(sc);
        scenarioId = created.id;
      } else {
        await api.updateScenario(id, sc);
      }
      setRunning(true);
      const { run_id } = await api.startRun(scenarioId, nUsers);
      router.push(`/run/${run_id}`);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Failed to start run');
    } finally {
      setSaving(false);
      setRunning(false);
    }
  };

  const setMsg = (i: number, v: string) => setMessages((m) => m.map((x, j) => (j === i ? v : x)));
  const addMsg = () => setMessages((m) => [...m, '']);
  const removeMsg = (i: number) => setMessages((m) => m.filter((_, j) => j !== i));

  return (
    <div className="p-8 max-w-2xl">
      <div className="flex items-center gap-3 mb-6">
        <button className="text-gray-500 hover:text-white text-sm" onClick={() => router.push('/scenarios')}>← Back</button>
        <h1 className="text-2xl font-semibold">{isNew ? 'New Scenario' : 'Edit Scenario'}</h1>
      </div>

      {error && <div className="mb-4 p-3 bg-red-900/30 border border-red-700 rounded-lg text-red-300 text-sm">{error}</div>}

      <div className="space-y-6">
        <Field label="Scenario Name">
          <input className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. payops debator smoke" />
        </Field>

        <div className="grid grid-cols-3 gap-4">
          <Field label="Tenant">
            <select className="input" value={tenantSlug} onChange={(e) => setTenantSlug(e.target.value)}>
              <option value="">Select…</option>
              {tenants.map((t) => <option key={t.id} value={t.slug}>{t.display_name}</option>)}
            </select>
          </Field>

          <Field label="Application">
            <select className="input" value={appID} onChange={(e) => {
              const app = apps.find(a => a.id === e.target.value);
              setAppID(e.target.value);
              setAppSlug(app?.slug ?? '');
            }} disabled={!tenantSlug}>
              <option value="">Select…</option>
              {apps.map((a) => <option key={a.id} value={a.id}>{a.name}</option>)}
            </select>
          </Field>

          <Field label="Entry Point">
            <select className="input" value={epSlug} onChange={(e) => setEPSlug(e.target.value)} disabled={!appID}>
              <option value="">Select…</option>
              {eps.map((ep) => <option key={ep.id} value={ep.slug}>{ep.slug} ({ep.entry_point_type})</option>)}
            </select>
          </Field>
        </div>

        <Field label="Auth Mode">
          <div className="grid grid-cols-2 gap-2">
            {AUTH_MODES.map((m) => (
              <label key={m.value} className={`flex items-start gap-3 p-3 rounded-lg border cursor-pointer transition-colors ${authMode === m.value ? 'border-indigo-500 bg-indigo-900/20' : 'border-gray-700 hover:border-gray-600'}`}>
                <input type="radio" name="auth_mode" value={m.value} checked={authMode === m.value} onChange={() => setAuthMode(m.value as Scenario['auth_mode'])} className="mt-0.5" />
                <div>
                  <div className="text-sm font-medium">{m.label}</div>
                  <div className="text-xs text-gray-400">{m.desc}</div>
                </div>
              </label>
            ))}
          </div>
        </Field>

        <Field label="Virtual Users" hint="Each user gets their own bearer token and WS connection">
          <div className="flex items-center gap-3">
            <input
              type="range" min={1} max={50} value={nUsers}
              onChange={(e) => setNUsers(Number(e.target.value))}
              className="flex-1 accent-indigo-500"
            />
            <span className="text-indigo-400 font-mono w-8 text-right">{nUsers}</span>
          </div>
        </Field>

        <Field label="Message Script" hint="Messages sent in order by each virtual user">
          <div className="space-y-2">
            {messages.map((m, i) => (
              <div key={i} className="flex gap-2">
                <span className="text-gray-600 text-sm w-5 pt-2.5 shrink-0">{i + 1}.</span>
                <input className="input flex-1" value={m} onChange={(e) => setMsg(i, e.target.value)} placeholder="Enter message…" />
                <button onClick={() => removeMsg(i)} className="text-gray-600 hover:text-red-400 px-2" disabled={messages.length === 1}>×</button>
              </div>
            ))}
            <button onClick={addMsg} className="text-indigo-400 text-sm hover:text-indigo-300">+ Add message</button>
          </div>
        </Field>

        <div className="flex gap-3 pt-2">
          <button className="btn-primary" onClick={save} disabled={saving || !name || !epSlug}>
            {saving ? 'Saving…' : 'Save'}
          </button>
          <button className="btn-run" onClick={run} disabled={saving || running || !name || !epSlug}>
            {running ? 'Starting…' : '▶ Save & Run'}
          </button>
          <button className="btn-secondary" onClick={() => router.push('/scenarios')}>Cancel</button>
        </div>
      </div>
    </div>
  );
}

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div>
      <label className="block text-sm font-medium text-gray-300 mb-1.5">{label}</label>
      {children}
      {hint && <p className="text-xs text-gray-500 mt-1">{hint}</p>}
    </div>
  );
}
