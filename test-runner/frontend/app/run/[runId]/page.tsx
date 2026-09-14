'use client';
import { useParams, useRouter } from 'next/navigation';
import { useRunStream } from '@/lib/useSSE';
import type { UserResult } from '@/lib/types';

export default function RunPage() {
  const { runId } = useParams<{ runId: string }>();
  const router = useRouter();
  const { users, summary, done } = useRunStream(runId);

  const passed = users.filter((u) => u.status === 'passed').length;
  const failed = users.filter((u) => u.status === 'failed').length;
  const running = users.filter((u) => u.status === 'running').length;
  const total = summary?.n_users ?? users.length;

  return (
    <div className="p-8">
      <div className="flex items-center gap-4 mb-6">
        <button className="text-gray-500 hover:text-white text-sm" onClick={() => router.push('/scenarios')}>← Scenarios</button>
        <div>
          <h1 className="text-2xl font-semibold">{summary?.scenario_name ?? 'Running…'}</h1>
          <div className="text-xs text-gray-500 font-mono mt-0.5">{runId}</div>
        </div>
        <div className="ml-auto flex gap-4 text-sm">
          {!done && running > 0 && <span className="text-yellow-400 animate-pulse">● {running} running</span>}
          <span className="text-green-400">✓ {passed} passed</span>
          <span className="text-red-400">✗ {failed} failed</span>
          {total > 0 && <span className="text-gray-400">/ {total} total</span>}
        </div>
      </div>

      {done && summary && (
        <div className={`mb-6 p-4 rounded-lg border text-sm ${summary.failed === 0 ? 'bg-green-900/20 border-green-700' : 'bg-red-900/20 border-red-700'}`}>
          {summary.failed === 0
            ? `✓ All ${summary.passed} users passed in ${elapsed(summary.started_at, summary.ended_at)}`
            : `${summary.passed} passed, ${summary.failed} failed — ${elapsed(summary.started_at, summary.ended_at)}`}
          <button className="ml-4 text-gray-400 hover:text-white underline" onClick={() => router.push(`/history/${runId}`)}>View full report →</button>
        </div>
      )}

      {users.length === 0 && !done && (
        <div className="text-gray-500 text-sm animate-pulse">Connecting virtual users…</div>
      )}

      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-3">
        {users.map((u) => (
          <UserCard key={u.user_index} user={u} />
        ))}
      </div>
    </div>
  );
}

function UserCard({ user }: { user: UserResult }) {
  const statusColor = {
    running: 'border-yellow-600 bg-yellow-900/10',
    passed: 'border-green-600 bg-green-900/10',
    failed: 'border-red-600 bg-red-900/10',
  }[user.status];

  const statusBadge = {
    running: <span className="text-yellow-400 text-xs animate-pulse">● running</span>,
    passed: <span className="text-green-400 text-xs">✓ passed</span>,
    failed: <span className="text-red-400 text-xs">✗ failed</span>,
  }[user.status];

  return (
    <div className={`rounded-xl border p-4 ${statusColor} flex flex-col gap-3`}>
      <div className="flex items-center justify-between">
        <span className="font-mono text-sm text-gray-300">User {user.user_index + 1}</span>
        {statusBadge}
      </div>

      {user.connected && (
        <div className="text-xs text-gray-500">
          🔌 Connected {user.duration_ms > 0 ? `· ${user.duration_ms}ms` : ''}
        </div>
      )}

      {user.error && (
        <div className="text-xs text-red-400 bg-red-900/20 rounded p-2">{user.error}</div>
      )}

      <div className="space-y-2">
        {user.steps.map((step, i) => (
          <div key={i} className="text-xs space-y-1">
            <div className="text-gray-400 truncate">→ {step.sent}</div>
            {step.received && (
              <div className="text-gray-300 line-clamp-3 leading-relaxed pl-2 border-l border-gray-700">
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

function elapsed(start?: string, end?: string) {
  if (!start || !end) return '';
  const ms = new Date(end).getTime() - new Date(start).getTime();
  return ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(1)}s`;
}
