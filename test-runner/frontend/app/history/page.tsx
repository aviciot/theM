'use client';
import { useEffect, useState } from 'react';
import Link from 'next/link';
import { api } from '@/lib/api';
import type { RunSummary } from '@/lib/types';

export default function HistoryPage() {
  const [runs, setRuns] = useState<RunSummary[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    api.listHistory().then((r) => { setRuns(r.reverse()); setLoading(false); });
  }, []);

  const del = async (runId: string) => {
    await api.deleteHistory(runId);
    setRuns((r) => r.filter((x) => x.run_id !== runId));
  };

  return (
    <div className="p-8">
      <h1 className="text-2xl font-semibold mb-1">History</h1>
      <p className="text-gray-400 text-sm mb-6">Past test run results</p>

      {loading && <p className="text-gray-500 text-sm">Loading…</p>}
      {!loading && runs.length === 0 && (
        <p className="text-gray-500 text-sm">No runs yet. Go run a scenario.</p>
      )}

      <div className="space-y-2">
        {runs.map((run) => (
          <div key={run.run_id} className="card flex items-center justify-between">
            <div>
              <div className="font-medium">{run.scenario_name}</div>
              <div className="text-xs text-gray-400 mt-0.5 font-mono">{run.started_at}</div>
            </div>
            <div className="flex items-center gap-4">
              <div className="text-sm">
                <span className="text-green-400">{run.passed} passed</span>
                {run.failed > 0 && <span className="text-red-400 ml-2">{run.failed} failed</span>}
                <span className="text-gray-500 ml-2">/ {run.n_users} users</span>
              </div>
              <Link href={`/history/${run.run_id}`} className="btn-secondary text-sm">View</Link>
              <button className="btn-danger text-sm" onClick={() => del(run.run_id)}>Delete</button>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
