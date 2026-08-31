'use client';
import { useState, useEffect } from 'react';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { themApi, type Application, type Agent } from '@/lib/api';
import { C } from './constants';
import { CanvasBuilderView } from './components/CanvasBuilderView';
import { RuntimeView } from './components/RuntimeView';
import { SessionsView } from './components/SessionsView';
import { MCPCredentialsView } from './components/MCPCredentialsView';
import { MonitorView } from './components/MonitorView';
import { ListView } from './components/ListView';

// ── Page root ─────────────────────────────────────────────────────────────────
export default function ApplicationsPage() {
  const [list, setList] = useState<Application[]>([]);
  const [agents, setAgents] = useState<Agent[]>([]);
  const [loading, setLoading] = useState(true);
  const [view, setView] = useState<'list' | 'definition' | 'sessions' | 'runtime' | 'mcp-credentials' | 'monitor'>('list');
  const [definitionApp, setDefinitionApp] = useState<Application | null>(null);
  const [sessionsApp, setSessionsApp] = useState<Application | null>(null);
  const [runtimeApp, setRuntimeApp] = useState<Application | null>(null);
  const [mcpApp, setMcpApp] = useState<Application | null>(null);
  const [monitorApp, setMonitorApp] = useState<Application | null>(null);
  const [token, setToken] = useState<string | null>(null);
  useEffect(() => {
    fetch('/api/auth/token').then(r => r.ok ? r.json() : null).then(d => { if (d?.token) setToken(d.token); }).catch(() => {});
  }, []);
  const [selectedApps, setSelectedApps] = useState<Set<string>>(new Set());
  const [bulkDeleting, setBulkDeleting] = useState(false);
  const [pageToast, setPageToast] = useState<{ msg: string; ok: boolean } | null>(null);

  function showListToast(msg: string, ok: boolean) {
    setPageToast({ msg, ok });
    setTimeout(() => setPageToast(null), 3000);
  }

  async function load() {
    setLoading(true);
    try {
      const [apps, ags] = await Promise.all([themApi.applications(), themApi.agents()]);
      setList(apps);
      setAgents(ags);
    } catch {
      // Transient auth race on first mount (token not yet in cookie) — retry once
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => { load(); }, []);

  async function handleToggle(app: Application) {
    try {
      const updated = await themApi.updateApplication(app.id, { name: app.name, slug: app.slug, enabled: !app.enabled });
      setList(prev => prev.map(a => a.id === app.id ? { ...a, ...updated, enabled: !app.enabled } : a));
    } catch {/* ignore — AppCard shows toggling state */}
  }

  async function handleDelete(app: Application) {
    try {
      await themApi.deleteApplication(app.id);
      await load();
      showListToast(`"${app.name}" deleted`, true);
    } catch (e) {
      showListToast(e instanceof Error ? e.message : 'Delete failed', false);
    }
  }

  function handleToggleSelect(id: string, checked: boolean) {
    setSelectedApps(prev => {
      const next = new Set(prev);
      checked ? next.add(id) : next.delete(id);
      return next;
    });
  }

  function handleSelectAll(checked: boolean) {
    setSelectedApps(checked ? new Set(list.map(a => a.id)) : new Set());
  }

  async function handleBulkDelete() {
    if (selectedApps.size === 0) return;
    setBulkDeleting(true);
    try {
      await themApi.bulkDeleteApplications(Array.from(selectedApps));
      setSelectedApps(new Set());
      await load();
    } catch (e) {
      showListToast(e instanceof Error ? e.message : 'Bulk delete failed', false);
    } finally {
      setBulkDeleting(false);
    }
  }

  function openDefinition(app: Application) {
    setDefinitionApp(app);
    setView('definition');
  }

  function backToList() {
    setView('list');
    setDefinitionApp(null);
    setSessionsApp(null);
    setRuntimeApp(null);
    setMcpApp(null);
    setMonitorApp(null);
  }

  function openMonitor(app: Application) {
    setMonitorApp(app);
    setView('monitor');
  }

  function openMCPCredentials(app: Application) {
    setMcpApp(app);
    setView('mcp-credentials');
  }

  function openSessions(app: Application) {
    setSessionsApp(app);
    setView('sessions');
  }

  function openRuntime(app: Application) {
    setRuntimeApp(app);
    setView('runtime');
  }

  if (view === 'monitor' && monitorApp) {
    return (
      <AuthGuard>
        <div style={{ display: 'flex', minHeight: '100vh', background: C.bg }}>
          <Sidebar />
          <div style={{ marginLeft: 260, flex: 1, display: 'flex', flexDirection: 'column', height: '100vh', overflow: 'hidden' }}>
            <MonitorView app={monitorApp} token={token} onBack={backToList} />
          </div>
        </div>
      </AuthGuard>
    );
  }

  if (view === 'mcp-credentials' && mcpApp) {
    return (
      <AuthGuard>
        <div style={{ display: 'flex', minHeight: '100vh', background: C.bg }}>
          <Sidebar />
          <div style={{ marginLeft: 260, flex: 1, display: 'flex', flexDirection: 'column', height: '100vh', overflow: 'hidden' }}>
            <MCPCredentialsView app={mcpApp} onBack={backToList} />
          </div>
        </div>
      </AuthGuard>
    );
  }

  if (view === 'runtime' && runtimeApp) {
    return (
      <AuthGuard>
        <div style={{ display: 'flex', minHeight: '100vh', background: C.bg }}>
          <Sidebar />
          <div style={{ marginLeft: 260, flex: 1, display: 'flex', flexDirection: 'column', height: '100vh', overflow: 'hidden' }}>
            <RuntimeView
              app={runtimeApp}
              onBack={backToList}
            />
          </div>
        </div>
      </AuthGuard>
    );
  }

  if (view === 'sessions' && sessionsApp) {
    return (
      <AuthGuard>
        <div style={{ display: 'flex', minHeight: '100vh', background: C.bg }}>
          <Sidebar />
          <div style={{ marginLeft: 260, flex: 1, display: 'flex', flexDirection: 'column', height: '100vh', overflow: 'hidden' }}>
            <SessionsView
              app={sessionsApp}
              agents={agents}
              onBack={backToList}
              token={token}
            />
          </div>
        </div>
      </AuthGuard>
    );
  }

  if (view === 'definition' && definitionApp) {
    return (
      <AuthGuard>
        <div style={{ display: 'flex', minHeight: '100vh', background: C.bg }}>
          <Sidebar />
          <div style={{ marginLeft: 260, flex: 1, display: 'flex', flexDirection: 'column', height: '100vh', overflow: 'hidden' }}>
            <CanvasBuilderView
              app={definitionApp}
              agents={agents}
              onBack={() => { setView('list'); setDefinitionApp(null); }}
              onAppUpdated={(updated) => {
                setList(prev => prev.map(a => a.id === updated.id ? updated : a));
                setDefinitionApp(updated);
              }}
            />
          </div>
        </div>
      </AuthGuard>
    );
  }

  return (
    <AuthGuard>
      <div style={{ display: 'flex', minHeight: '100vh', background: C.bg }}>
        <Sidebar />
        <ListView
          list={list}
          loading={loading}
          onNew={async () => {
            try {
              const app = await themApi.createApplication({ name: `New Application ${Date.now()}`, enabled: false });
              await load();
              openDefinition(app);
            } catch {/* ignore */}
          }}
          onEdit={(app) => openDefinition(app)}
          onSessions={openSessions}
          onRuntime={openRuntime}
          onMCPCredentials={openMCPCredentials}
          onMonitor={openMonitor}
          onToggle={handleToggle}
          onDelete={handleDelete}
          onReload={load}
          selectedApps={selectedApps}
          onToggleSelect={handleToggleSelect}
          onSelectAll={handleSelectAll}
          onBulkDelete={handleBulkDelete}
          bulkDeleting={bulkDeleting}
        />
        {pageToast && (
          <div style={{
            position: 'fixed', bottom: 24, right: 24, zIndex: 9999,
            background: pageToast.ok ? C.greenBg : 'rgba(239,68,68,0.12)',
            border: `1px solid ${pageToast.ok ? C.greenBorder : 'rgba(239,68,68,0.3)'}`,
            color: pageToast.ok ? C.green : '#f87171',
            borderRadius: 10, padding: '10px 20px', fontSize: 13, fontWeight: 600,
          }}>
            {pageToast.msg}
          </div>
        )}
      </div>
    </AuthGuard>
  );
}
