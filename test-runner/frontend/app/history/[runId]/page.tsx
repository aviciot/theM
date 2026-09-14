'use client';
import { useEffect, useState } from 'react';
import { useParams, useRouter } from 'next/navigation';
import { api } from '@/lib/api';
import type { RunSummary, UserResult } from '@/lib/types';

export default function HistoryDetailPage() {
  const { runId } = useParams<{ runId: string }>();
  const router = useRouter();
  const [run, setRun] = useState<RunSummary | null>(null);

  useEffect(() => {
    api.getHistory(runId).then(setRun);
  }, [runId]);

  if (!run) return <div className="p-8 text-gray-500">Loading…</div>;

  return (
    <div className="p-8">
      <div className="flex items-center gap-4 mb-6">
        <button className="text-gray-500 hover:text-white text-sm" onClick={() => router.push('/history')}>← History</button>
        <div>
          <h1 className="text-2xl font-semibold">{run.scenario_name}</h1>
          <div className="text-xs text-gray-500 font-mono mt-0.5">{run.run_id}</div>
        </div>
        <div className="ml-auto flex gap-4 text-sm">
          <span className="text-green-400">✓ {run.passed} passed</span>
          {run.failed > 0 && <span className="text-red-400">✗ {run.failed} failed</span>}
          <span className="text-gray-400">/ {run.n_users} total</span>
        </div>
      </div>

      <div className="text-xs text-gray-500 mb-6">
        {run.started_at} → {run.ended_at}
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-3">
        {(run.results ?? []).map((u) => (
          <UserDetailCard key={u.user_index} user={u} />
        ))}
      </div>
    </div>
  );
}

function UserDetailCard({ user }: { user: UserResult }) {
  const statusColor = {
    running: 'border-yellow-600',
    passed: 'border-green-600 bg-green-900/10',
    failed: 'border-red-600 bg-red-900/10',
  }[user.status] ?? 'border-gray-700';

  return (
    <div className={`rounded-xl border p-4 ${statusColor} flex flex-col gap-3`}>
      <div className="flex items-center justify-between">
        <span className="font-mono text-sm text-gray-300">User {user.user_index + 1}</span>
        <span className={`text-xs ${user.status === 'passed' ? 'text-green-400' : 'text-red-400'}`}>
          {user.status === 'passed' ? '✓ passed' : '✗ failed'}
        </span>
      </div>
      <div className="text-xs text-gray-500">{user.duration_ms}ms total</div>
      {user.error && <div className="text-xs text-red-400">{user.error}</div>}
      <div className="space-y-3">
        {(user.steps ?? []).map((step, i) => (
          <div key={i} className="text-xs space-y-1.5">
            <div className="text-gray-400 font-medium">→ {step.sent}</div>
            {step.received && (
              <div className="text-gray-300 leading-relaxed pl-2 border-l border-gray-700 whitespace-pre-wrap">
                {step.received}
              </div>
            )}
            {step.error && <div className="text-red-400">{step.error}</div>}
            <div className="text-gray-600">{step.latency_ms}ms {step.ok ? '✓' : '✗'}</div>
          </div>
        ))}
      </div>
    </div>
  );
}
