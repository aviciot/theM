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
    api.listScenarios()
      .then((s) => {
        console.log('Scenarios loaded:', s.length);
        setScenarios(s);
        setLoading(false);
      })
      .catch((err) => {
        console.error('Failed to load scenarios:', err);
        setLoading(false);
      });
  }, []);

  const del = async (id: string) => {
    if (!confirm('Delete this scenario?')) return;
    await api.deleteScenario(id);
    setScenarios((s) => s.filter((x) => x.id !== id));
  };

  const gateway = scenarios.filter(s => s.ep_type === 'gateway');
  const agentic = scenarios.filter(s => s.ep_type !== 'gateway');

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

      {!loading && scenarios.length > 0 && (
        <div className="space-y-8">
          {gateway.length > 0 && (
            <section>
              <div className="flex items-center gap-2 mb-3">
                <span className="text-xs font-bold uppercase tracking-widest text-purple-400">Gateway Profile</span>
                <span className="text-xs text-gray-600">— OpenAI-compat endpoint tests</span>
              </div>
              <div className="grid gap-3">
                {gateway.map(sc => <ScenarioCard key={sc.id} sc={sc} onDelete={del} onEdit={id => router.push(`/scenarios/${id}`)} />)}
              </div>
            </section>
          )}
          {agentic.length > 0 && (
            <section>
              <div className="flex items-center gap-2 mb-3">
                <span className="text-xs font-bold uppercase tracking-widest text-indigo-400">Agentic App</span>
                <span className="text-xs text-gray-600">— WS / SSE / A2A endpoint tests</span>
              </div>
              <div className="grid gap-3">
                {agentic.map(sc => <ScenarioCard key={sc.id} sc={sc} onDelete={del} onEdit={id => router.push(`/scenarios/${id}`)} />)}
              </div>
            </section>
          )}
        </div>
      )}
    </div>
  );
}

function ScenarioCard({ sc, onDelete, onEdit }: { sc: Scenario; onDelete: (id: string) => void; onEdit: (id: string) => void }) {
  const isGateway = sc.ep_type === 'gateway';
  const hasAnalysis = sc.test_result_ai_analysis && sc.test_result_ai_analysis.trim().length > 0;
  const [showDescription, setShowDescription] = useState(false);
  const [showAnalysis, setShowAnalysis] = useState(false);

  return (
    <div className="card">
      <div className="flex items-center justify-between mb-2">
        <div className="flex-1">
          <div className="flex items-center gap-2">
            <span className="font-semibold text-base">{sc.name}</span>
            {isGateway && (
              <span className="text-xs font-semibold px-1.5 py-0.5 rounded bg-purple-900/40 text-purple-300 border border-purple-700/50">
                {sc.stream ? 'streaming' : 'non-streaming'}
              </span>
            )}
          </div>
          <div className="text-xs text-gray-400 mt-1">
            {isGateway ? (
              <>{sc.tenant_slug} &nbsp;·&nbsp; <span className="text-purple-400">gateway</span> &nbsp;·&nbsp; <span className="font-mono text-purple-300">{sc.model ?? 'claude-sonnet-4-6'}</span> &nbsp;·&nbsp; {sc.n_users} user{sc.n_users !== 1 ? 's' : ''} &nbsp;·&nbsp; {sc.messages.length} turn{sc.messages.length !== 1 ? 's' : ''}</>
            ) : (
              <>{sc.tenant_slug} / {sc.app_slug} / {sc.ep_slug} &nbsp;·&nbsp; <span className="text-indigo-400">{sc.auth_mode}</span> &nbsp;·&nbsp; {sc.n_users} user{sc.n_users !== 1 ? 's' : ''} &nbsp;·&nbsp; {sc.messages.length} message{sc.messages.length !== 1 ? 's' : ''}</>
            )}
          </div>
        </div>
        <div className="flex gap-2 ml-4 shrink-0">
          <button className="btn-secondary text-sm" onClick={() => onEdit(sc.id)}>Edit</button>
          <RunButton scenarioId={sc.id} nUsers={sc.n_users} />
          <button className="btn-danger text-sm" onClick={() => onDelete(sc.id)}>Delete</button>
        </div>
      </div>

      {sc.description && (
        <div className="mb-2">
          <button
            onClick={() => setShowDescription(!showDescription)}
            className="flex items-center gap-2 text-xs font-semibold text-blue-400 hover:text-blue-300 transition-colors w-full text-left py-1"
          >
            <span className="transform transition-transform" style={{ display: 'inline-block', transform: showDescription ? 'rotate(90deg)' : 'rotate(0deg)' }}>▶</span>
            <span className="uppercase tracking-wide">📝 What This Tests</span>
          </button>
          {showDescription && (
            <div className="mt-2 p-3 rounded-lg bg-slate-900/30 border border-slate-800">
              <div className="text-sm text-gray-300 leading-relaxed whitespace-pre-line">
                {sc.description}
              </div>
            </div>
          )}
        </div>
      )}

      <div className="mt-2 pt-2 border-t border-gray-800">
        <button
          onClick={() => setShowAnalysis(!showAnalysis)}
          className="flex items-center gap-2 text-xs font-semibold text-amber-400 hover:text-amber-300 transition-colors w-full text-left py-1"
        >
          <span className="transform transition-transform" style={{ display: 'inline-block', transform: showAnalysis ? 'rotate(90deg)' : 'rotate(0deg)' }}>▶</span>
          <span className="uppercase tracking-wide">📊 Test Result Analysis</span>
          {hasAnalysis && <span className="text-xs text-green-400 ml-2">✓ Has Results</span>}
        </button>
        {showAnalysis && (
          <div className="mt-2">
            {hasAnalysis ? (
              <div className="text-sm text-gray-300 leading-relaxed whitespace-pre-line bg-slate-900/30 p-3 rounded-lg border border-slate-800">
                {sc.test_result_ai_analysis}
              </div>
            ) : (
              <div className="text-xs text-gray-500 italic p-3 bg-slate-900/20 rounded border border-dashed border-gray-700">
                No analysis yet — run this test and the results will appear here
              </div>
            )}
          </div>
        )}
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
