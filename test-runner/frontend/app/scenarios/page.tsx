'use client';
import { useEffect, useState } from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { api } from '@/lib/api';
import type { Scenario } from '@/lib/types';

export default function ScenariosPage() {
  const [scenarios, setScenarios] = useState<Scenario[]>([]);
  const [loading, setLoading] = useState(true);
  const router = useRouter();

  useEffect(() => {
    api.listScenarios().then((s) => { setScenarios(s); setLoading(false); });
  }, []);

  const del = async (id: string) => {
    if (!confirm('Delete this scenario?')) return;
    await api.deleteScenario(id);
    setScenarios((s) => s.filter((x) => x.id !== id));
  };

  return (
    <div className="p-8">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-2xl font-semibold">Scenarios</h1>
          <p className="text-gray-400 text-sm mt-0.5">Configure and run end-user test scenarios</p>
        </div>
        <Link href="/scenarios/new" className="btn-primary">+ New Scenario</Link>
      </div>

      {loading && <p className="text-gray-500 text-sm">Loading…</p>}
      {!loading && scenarios.length === 0 && (
        <div className="border border-dashed border-gray-700 rounded-xl p-12 text-center">
          <p className="text-gray-500 mb-4">No scenarios yet.</p>
          <Link href="/scenarios/new" className="btn-primary">Create your first scenario</Link>
        </div>
      )}

      <div className="grid gap-3">
        {scenarios.map((sc) => (
          <div key={sc.id} className="card flex items-center justify-between">
            <div>
              <div className="font-medium">{sc.name}</div>
              <div className="text-xs text-gray-400 mt-0.5">
                {sc.tenant_slug} / {sc.app_slug} / {sc.ep_slug} &nbsp;·&nbsp;
                <span className="text-indigo-400">{sc.auth_mode}</span> &nbsp;·&nbsp;
                {sc.n_users} user{sc.n_users !== 1 ? 's' : ''} &nbsp;·&nbsp;
                {sc.messages.length} message{sc.messages.length !== 1 ? 's' : ''}
              </div>
            </div>
            <div className="flex gap-2">
              <button className="btn-secondary text-sm" onClick={() => router.push(`/scenarios/${sc.id}`)}>Edit</button>
              <RunButton scenarioId={sc.id} nUsers={sc.n_users} />
              <button className="btn-danger text-sm" onClick={() => del(sc.id)}>Delete</button>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

function RunButton({ scenarioId, nUsers }: { scenarioId: string; nUsers: number }) {
  const router = useRouter();
  const [running, setRunning] = useState(false);

  const run = async () => {
    setRunning(true);
    try {
      const { run_id } = await api.startRun(scenarioId, nUsers);
      router.push(`/run/${run_id}`);
    } finally {
      setRunning(false);
    }
  };

  return (
    <button className="btn-run text-sm" onClick={run} disabled={running}>
      {running ? '…' : '▶ Run'}
    </button>
  );
}
