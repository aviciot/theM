'use client';

import { useEffect, useState, useCallback } from 'react';
import { useRouter } from 'next/navigation';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { themApi, type AgentDefinition } from '@/lib/api';

// ── Helpers ────────────────────────────────────────────────────────────────────

function fmtDate(iso: string): string {
  if (!iso) return '—';
  try {
    return new Date(iso).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
  } catch {
    return iso;
  }
}

function StatusBadge({ status }: { status: string }) {
  const cls =
    status === 'published'
      ? 'bg-green-900/40 text-green-300 border border-green-700'
      : 'bg-yellow-900/40 text-yellow-300 border border-yellow-700';
  return (
    <span className={`inline-block text-xs px-2 py-0.5 rounded-full font-medium ${cls}`}>
      {status}
    </span>
  );
}

// ── Main page ──────────────────────────────────────────────────────────────────

export default function AgentLibraryPage() {
  const router = useRouter();
  const [defs, setDefs] = useState<AgentDefinition[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [search, setSearch] = useState('');
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [cloning, setCloning] = useState<string | null>(null);
  const [actionError, setActionError] = useState('');

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const data = await themApi.listAgentDefinitions();
      setDefs(data ?? []);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Failed to load agent library');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  const handleOpen = (def: AgentDefinition) => {
    router.push(`/admin/agents/builder?id=${def.id}`);
  };

  const handleClone = async (def: AgentDefinition) => {
    setCloning(def.id);
    setActionError('');
    try {
      const result = await themApi.cloneAgentDefinition(def.id);
      router.push(`/admin/agents/builder?id=${result.id}`);
    } catch (e: unknown) {
      setActionError(e instanceof Error ? e.message : 'Clone failed');
      setCloning(null);
    }
  };

  const handleDelete = async (id: string) => {
    setActionError('');
    try {
      await themApi.deleteAgentDefinition(id);
      setConfirmDelete(null);
      setDefs(prev => prev.filter(d => d.id !== id));
    } catch (e: unknown) {
      setActionError(e instanceof Error ? e.message : 'Delete failed');
      setConfirmDelete(null);
    }
  };

  const filtered = defs.filter(d => {
    const q = search.toLowerCase();
    return (
      !q ||
      d.display_name?.toLowerCase().includes(q) ||
      d.agent_slug?.toLowerCase().includes(q) ||
      d.owner_username?.toLowerCase().includes(q)
    );
  });

  return (
    <AuthGuard>
      <div className="flex h-screen bg-[#0a0a0a] text-gray-100 overflow-hidden">
        <Sidebar />
        <main className="flex-1 flex flex-col overflow-hidden">
          {/* Header */}
          <div className="flex items-center justify-between px-8 pt-8 pb-4 border-b border-gray-800 shrink-0">
            <div>
              <h1 className="text-2xl font-bold text-white">Agent Library</h1>
              <p className="text-sm text-gray-400 mt-1">
                All canvas agent definitions — draft and published
              </p>
            </div>
            <button
              onClick={() => router.push('/admin/agents/builder')}
              className="px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white text-sm font-medium rounded-lg transition-colors"
            >
              + New Agent
            </button>
          </div>

          {/* Search bar */}
          <div className="px-8 py-3 border-b border-gray-800 shrink-0">
            <input
              type="text"
              placeholder="Search by name, slug, or owner..."
              value={search}
              onChange={e => setSearch(e.target.value)}
              className="w-full max-w-md px-3 py-2 bg-gray-900 border border-gray-700 rounded-lg text-sm text-gray-100 placeholder-gray-500 focus:outline-none focus:border-blue-500"
            />
          </div>

          {/* Error banner */}
          {actionError && (
            <div className="mx-8 mt-3 px-4 py-2 bg-red-900/40 border border-red-700 text-red-300 text-sm rounded-lg shrink-0">
              {actionError}
            </div>
          )}

          {/* Content */}
          <div className="flex-1 overflow-y-auto px-8 py-4">
            {loading ? (
              <div className="text-gray-400 text-sm mt-8">Loading...</div>
            ) : error ? (
              <div className="text-red-400 text-sm mt-8">{error}</div>
            ) : filtered.length === 0 ? (
              <div className="text-gray-500 text-sm mt-8">
                {search ? 'No agents match your search.' : 'No agent definitions yet. Create your first agent above.'}
              </div>
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="text-left text-gray-500 border-b border-gray-800">
                      <th className="pb-2 pr-4 font-medium">Name</th>
                      <th className="pb-2 pr-4 font-medium">Slug</th>
                      <th className="pb-2 pr-4 font-medium">Status</th>
                      <th className="pb-2 pr-4 font-medium">Revision</th>
                      <th className="pb-2 pr-4 font-medium">Owner</th>
                      <th className="pb-2 pr-4 font-medium">Updated</th>
                      <th className="pb-2 font-medium text-right">Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {filtered.map(def => (
                      <tr
                        key={def.id}
                        className="border-b border-gray-800/50 hover:bg-gray-900/30 transition-colors"
                      >
                        <td className="py-3 pr-4">
                          <button
                            onClick={() => handleOpen(def)}
                            className="text-blue-400 hover:text-blue-300 font-medium text-left"
                          >
                            {def.display_name || def.agent_slug}
                          </button>
                        </td>
                        <td className="py-3 pr-4 text-gray-400 font-mono text-xs">
                          {def.agent_slug}
                        </td>
                        <td className="py-3 pr-4">
                          <StatusBadge status={def.status} />
                        </td>
                        <td className="py-3 pr-4 text-gray-400">
                          r{def.revision}
                        </td>
                        <td className="py-3 pr-4 text-gray-400">
                          {def.owner_username || <span className="text-gray-600">—</span>}
                        </td>
                        <td className="py-3 pr-4 text-gray-400">
                          {fmtDate(def.updated_at)}
                        </td>
                        <td className="py-3 text-right">
                          <div className="flex items-center justify-end gap-2">
                            <button
                              onClick={() => handleOpen(def)}
                              className="px-3 py-1 bg-gray-800 hover:bg-gray-700 text-gray-300 text-xs rounded transition-colors"
                            >
                              Open
                            </button>
                            <button
                              onClick={() => handleClone(def)}
                              disabled={cloning === def.id}
                              className="px-3 py-1 bg-gray-800 hover:bg-gray-700 text-gray-300 text-xs rounded transition-colors disabled:opacity-50"
                            >
                              {cloning === def.id ? 'Cloning...' : 'Clone'}
                            </button>
                            {def.status === 'draft' && (
                              <button
                                onClick={() => setConfirmDelete(def.id)}
                                className="px-3 py-1 bg-red-900/40 hover:bg-red-900/70 text-red-400 text-xs rounded transition-colors"
                              >
                                Delete
                              </button>
                            )}
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        </main>
      </div>

      {/* Delete confirmation modal */}
      {confirmDelete && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60">
          <div className="bg-gray-900 border border-gray-700 rounded-xl p-6 max-w-sm w-full mx-4">
            <h2 className="text-lg font-semibold text-white mb-2">Delete Agent Definition</h2>
            <p className="text-sm text-gray-400 mb-6">
              This will permanently delete the draft. Published agents are protected and cannot be deleted here.
            </p>
            <div className="flex gap-3 justify-end">
              <button
                onClick={() => setConfirmDelete(null)}
                className="px-4 py-2 text-sm text-gray-400 hover:text-gray-200 transition-colors"
              >
                Cancel
              </button>
              <button
                onClick={() => handleDelete(confirmDelete)}
                className="px-4 py-2 bg-red-600 hover:bg-red-700 text-white text-sm font-medium rounded-lg transition-colors"
              >
                Delete
              </button>
            </div>
          </div>
        </div>
      )}
    </AuthGuard>
  );
}
