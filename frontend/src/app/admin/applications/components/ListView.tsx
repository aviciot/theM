'use client';
import { useState, useEffect } from 'react';
import { themApi, type Application } from '@/lib/api';
import ChromaGrid from '@/components/ChromaGrid';
import { C, glass, APP_CARD_STYLES } from '../constants';
import { AppCard, useDashAppStatuses } from './AppCard';
import type { AppLiveness } from '../types';
import { useAuthStore } from '@/stores/authStore';

// ── Onboarding banner (shown when no applications exist) ──────────────────────

function GetStartedBanner({ onNew }: { onNew: () => void }) {
  const user = useAuthStore(s => s.user);
  const isSuperAdmin = user?.role === 'super_admin';

  const steps = [
    {
      icon: 'apps',
      title: 'Create an Application',
      body: 'An Application is the deployable unit — it holds entry points (WebSocket, SSE, A2A) that your users connect to.',
    },
    {
      icon: 'smart_toy',
      title: 'Configure Agents & Orchestrators',
      body: 'Bind an AI agent or canvas workflow to your application. Set the LLM provider, API key, and runtime parameters.',
    },
    {
      icon: 'link',
      title: 'Share the Entry Point',
      body: 'Copy the generated WebSocket or A2A URL and hand it to your client. Use the Playground to test before going live.',
    },
  ];

  return (
    <div style={{ gridColumn: '1 / -1', padding: '16px 0 48px' }}>
      {/* Hero */}
      <div style={{
        background: 'linear-gradient(135deg, rgba(99,102,241,0.08) 0%, rgba(0,209,255,0.04) 100%)',
        border: '1px solid rgba(99,102,241,0.2)', borderRadius: 20,
        padding: '40px 48px', marginBottom: 32, textAlign: 'center',
      }}>
        <div style={{ fontSize: 48, marginBottom: 16 }}>🚀</div>
        <h3 style={{ fontSize: 22, fontWeight: 800, color: C.text, margin: '0 0 10px 0', letterSpacing: '-0.02em' }}>
          {isSuperAdmin ? 'No applications yet — create your first one' : 'Welcome — let\'s set up your first application'}
        </h3>
        <p style={{ fontSize: 14, color: C.textMuted, margin: '0 0 28px 0', maxWidth: 520, marginLeft: 'auto', marginRight: 'auto' }}>
          {isSuperAdmin
            ? 'Applications are the deployable tenant units on this platform. Each one gets its own entry points, agents, and runtime configuration.'
            : 'An application connects your users to an AI agent or workflow. Follow the steps below to deploy your first one.'}
        </p>
        <button
          onClick={onNew}
          style={{
            display: 'inline-flex', alignItems: 'center', gap: 8,
            padding: '13px 28px', borderRadius: 10, border: 'none', cursor: 'pointer',
            background: '#00d1ff', color: '#000', fontSize: 15, fontWeight: 700,
            boxShadow: '0 0 24px rgba(0,209,255,0.4)',
          }}
        >
          <span style={{ fontSize: 20, lineHeight: 1 }}>+</span>
          Create Application
        </button>
      </div>

      {/* Three-step guide */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 20 }}>
        {steps.map((s, i) => (
          <div key={i} style={{
            background: 'rgba(255,255,255,0.02)', border: '1px solid rgba(255,255,255,0.06)',
            borderRadius: 14, padding: '24px 22px',
          }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 12 }}>
              <div style={{
                width: 36, height: 36, borderRadius: 10, flexShrink: 0,
                background: 'rgba(99,102,241,0.12)', border: '1px solid rgba(99,102,241,0.25)',
                display: 'flex', alignItems: 'center', justifyContent: 'center',
              }}>
                <span className="material-symbols-outlined" style={{ fontSize: 20, color: '#818cf8' }}>{s.icon}</span>
              </div>
              <span style={{ fontSize: 11, fontWeight: 700, color: 'rgba(129,140,248,0.7)', textTransform: 'uppercase', letterSpacing: '0.08em' }}>
                Step {i + 1}
              </span>
            </div>
            <div style={{ fontSize: 14, fontWeight: 700, color: C.text, marginBottom: 6 }}>{s.title}</div>
            <div style={{ fontSize: 13, color: C.textMuted, lineHeight: 1.55 }}>{s.body}</div>
          </div>
        ))}
      </div>
    </div>
  );
}

export function ListView({
  list, loading, onNew, onEdit, onRuntime, onMCPCredentials, onMonitor, onToggle, onDelete, onReload,
  selectedApps, onToggleSelect, onSelectAll, onBulkDelete, bulkDeleting,
}: {
  list: Application[];
  loading: boolean;
  onNew: () => void;
  onEdit: (app: Application) => void;
  onRuntime: (app: Application) => void;
  onMCPCredentials: (app: Application) => void;
  onMonitor: (app: Application) => void;
  onToggle: (app: Application) => void;
  onDelete: (app: Application) => void;
  onReload: () => void;
  selectedApps: Set<string>;
  onToggleSelect: (id: string, checked: boolean) => void;
  onSelectAll: (checked: boolean) => void;
  onBulkDelete: () => void;
  bulkDeleting: boolean;
}) {
  const [renameApp, setRenameApp] = useState<Application | null>(null);
  const [renameName, setRenameName] = useState('');
  const [renameSlug, setRenameSlug] = useState('');
  const [slugManual, setSlugManual] = useState(false);
  const [renaming, setRenaming] = useState(false);
  const [listToast, setListToast] = useState<{ msg: string; ok: boolean } | null>(null);

  function showListToast(msg: string, ok: boolean) {
    setListToast({ msg, ok });
    setTimeout(() => setListToast(null), 3000);
  }

  function slugify(name: string): string {
    return name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 48) || '';
  }

  function openRename(app: Application) {
    setRenameApp(app);
    setRenameName(app.name);
    setRenameSlug(app.slug ?? slugify(app.name));
    setSlugManual(false);
  }

  function handleRenameNameChange(name: string) {
    setRenameName(name);
    if (!slugManual) setRenameSlug(slugify(name));
  }

  async function commitRename() {
    if (!renameApp || !renameName.trim()) return;
    setRenaming(true);
    try {
      await themApi.updateApplication(renameApp.id, { name: renameName.trim(), slug: renameSlug.trim() || undefined, enabled: renameApp.enabled });
      setRenameApp(null);
      showListToast('Renamed', true);
      onReload();
    } catch (e) {
      showListToast(e instanceof Error && e.message.includes('409') ? 'Slug already in use' : 'Rename failed', false);
    } finally {
      setRenaming(false);
    }
  }

  // Read JWT for WS auth — same cookie the rest of the app uses
  const [token, setToken] = useState<string | null>(null);
  useEffect(() => {
    fetch('/api/auth/token').then(r => r.ok ? r.json() : null).then(d => {
      if (d?.token) setToken(d.token);
    }).catch(() => {});
  }, []);

  const appStatuses = useDashAppStatuses(token);

  // Track session counts per app via individual session WS subscriptions
  // We subscribe to each app's sessions channel and count
  const [sessionCounts, setSessionCounts] = useState<Record<string, number>>({});
  useEffect(() => {
    if (!token || list.length === 0) return;
    const wsBase = window.location.origin.replace(/^http/, 'ws').replace(/^https/, 'wss');
    const wsUrl = `${wsBase}/ws/dashboard?token=${token}`;
    let ws: WebSocket;
    let dead = false;

    function connect() {
      ws = new WebSocket(wsUrl);
      ws.onopen = () => {
        const channels = list.map(a => `sessions:${a.id}`);
        ws.send(JSON.stringify({ type: 'subscribe', channels }));
      };
      ws.onmessage = (ev) => {
        try {
          const msg = JSON.parse(ev.data);
          if (!msg.channel?.startsWith('sessions:')) return;
          const appId = msg.channel.slice('sessions:'.length);
          const evt = msg.event;
          if (evt?.type === 'session_snapshot') {
            setSessionCounts(prev => ({ ...prev, [appId]: (evt.sessions ?? []).length }));
          } else if (evt?.type === 'session_start') {
            setSessionCounts(prev => ({ ...prev, [appId]: (prev[appId] ?? 0) + 1 }));
          } else if (evt?.type === 'session_end') {
            setSessionCounts(prev => ({ ...prev, [appId]: Math.max(0, (prev[appId] ?? 1) - 1) }));
          }
        } catch {}
      };
      ws.onclose = () => { if (!dead) setTimeout(connect, 4000); };
      ws.onerror = () => ws.close();
    }
    connect();
    return () => { dead = true; ws?.close(); };
  }, [token, list]);

  return (
    <div style={{ marginLeft: 260, flex: 1, background: C.bg, minHeight: '100vh' }}>
      <style>{APP_CARD_STYLES}</style>

      {/* Page header */}
      <div style={{ padding: '40px 32px 24px', display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <div>
          <h2 style={{ fontSize: 40, fontWeight: 800, color: C.text, margin: '0 0 6px 0', letterSpacing: '-0.03em', lineHeight: 1.1 }}>
            Applications
          </h2>
          <p style={{ fontSize: 14, color: C.textMuted, margin: 0 }}>
            Compose orchestrators and entry points into deployable agentic applications.
          </p>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          {list.length > 0 && (
            <label style={{ display: 'flex', alignItems: 'center', gap: 6, cursor: 'pointer', fontSize: 13, color: C.textMuted }}>
              <input
                type="checkbox"
                checked={selectedApps.size > 0 && selectedApps.size === list.length}
                ref={el => { if (el) el.indeterminate = selectedApps.size > 0 && selectedApps.size < list.length; }}
                onChange={e => onSelectAll(e.target.checked)}
                style={{ accentColor: '#00d1ff' }}
              />
              All
            </label>
          )}
          {selectedApps.size > 0 && (
            <button
              onClick={onBulkDelete}
              disabled={bulkDeleting}
              style={{
                display: 'flex', alignItems: 'center', gap: 6,
                padding: '10px 18px', borderRadius: 8, border: '1px solid rgba(248,113,113,0.4)',
                background: 'rgba(248,113,113,0.08)', color: '#f87171',
                fontSize: 13, fontWeight: 600, cursor: bulkDeleting ? 'not-allowed' : 'pointer',
                opacity: bulkDeleting ? 0.6 : 1, transition: 'opacity 0.15s',
              }}
            >
              {bulkDeleting ? 'Deleting…' : `Delete selected (${selectedApps.size})`}
            </button>
          )}
          <button
            onClick={onNew}
            style={{
              display: 'flex', alignItems: 'center', gap: 8,
              padding: '12px 24px', borderRadius: 8, border: 'none', cursor: 'pointer',
              background: '#00d1ff', color: '#000', fontSize: 14, fontWeight: 700,
              boxShadow: '0 0 20px rgba(0,209,255,0.4)',
            }}
          >
            <span style={{ fontSize: 18, lineHeight: 1 }}>+</span>
            New Application
          </button>
        </div>
      </div>

      {/* Card grid */}
      <ChromaGrid radius={420} damping={0.09} fadeOutMs={800} style={{ padding: '0 32px 48px' }}>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 24 }}>
        {loading && (
          <div style={{ gridColumn: '1 / -1', padding: 80, textAlign: 'center', color: C.textMuted, fontSize: 14 }}>
            Loading…
          </div>
        )}

        {!loading && list.length === 0 && (
          <GetStartedBanner onNew={onNew} />
        )}

        {!loading && list.map((app) => {
          // Aggregate liveness across all EPs: live if ANY is reachable, best latency wins.
          const epStatuses = (app.entry_points ?? [])
            .map(ep => appStatuses[ep.slug])
            .filter(Boolean) as AppLiveness[];
          const anyReachable = epStatuses.some(s => s.reachable);
          const allChecked = epStatuses.length > 0;
          const bestLatency = epStatuses
            .filter(s => s.reachable && s.latency_ms != null)
            .reduce((min, s) => (s.latency_ms! < min ? s.latency_ms! : min), Infinity);
          const aggLiveness: AppLiveness | null = allChecked
            ? { reachable: anyReachable, latency_ms: isFinite(bestLatency) ? bestLatency : null }
            : null;
          return (
          <AppCard
            key={app.id}
            app={app}
            liveness={aggLiveness}
            sessionCount={sessionCounts[app.id] ?? 0}
            selected={selectedApps.has(app.id)}
            onToggleSelect={onToggleSelect}
            onEdit={onEdit}
            onRuntime={onRuntime}
            onMCPCredentials={onMCPCredentials}
            onMonitor={onMonitor}
            onToggle={onToggle}
            onDelete={onDelete}
            onRename={openRename}
          />
          );
        })}

        {/* Deploy / New card — always last */}
        {!loading && list.length > 0 && (
          <div
            className="app-deploy-card"
            onClick={onNew}
            style={{
              borderRadius: 16, border: '2px dashed rgba(99,102,241,0.35)',
              background: 'rgba(99,102,241,0.02)',
              display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center',
              gap: 14, cursor: 'pointer', minHeight: 220, transition: 'border-color 200ms ease, background 200ms ease',
            }}
          >
            <div style={{ width: 52, height: 52, borderRadius: 14, border: '2px dashed rgba(99,102,241,0.5)', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
              <span className="material-icons" style={{ fontSize: 26, color: '#818cf8' }}>add</span>
            </div>
            <div style={{ fontSize: 14, fontWeight: 700, color: '#818cf8' }}>New Application</div>
          </div>
        )}
      </div>
      </ChromaGrid>

      {/* Rename Modal */}
      {renameApp && (
        <div
          style={{ position: 'fixed', top: 0, left: 0, width: '100%', height: '100%', background: 'rgba(5,20,36,0.85)', zIndex: 200, display: 'flex', alignItems: 'center', justifyContent: 'center' }}
          onClick={() => !renaming && setRenameApp(null)}
        >
          <div
            style={{ ...glass, borderRadius: 16, padding: '28px 32px', minWidth: 360, maxWidth: 480, position: 'relative' }}
            onClick={e => e.stopPropagation()}
          >
            <div style={{ fontSize: 14, fontWeight: 700, color: C.text, marginBottom: 16 }}>Rename Application</div>
            <div style={{ fontSize: 11, color: C.textMuted, marginBottom: 4 }}>Name</div>
            <input
              autoFocus
              value={renameName}
              onChange={e => handleRenameNameChange(e.target.value)}
              onKeyDown={e => { if (e.key === 'Enter') commitRename(); if (e.key === 'Escape') setRenameApp(null); }}
              placeholder="Application name"
              style={{ width: '100%', padding: '10px 14px', borderRadius: 8, border: `1px solid ${C.outlineVariant}`, background: C.surfaceContainer, color: C.text, fontSize: 14, outline: 'none', boxSizing: 'border-box', marginBottom: 12 }}
            />
            <div style={{ fontSize: 11, color: C.textMuted, marginBottom: 4 }}>URL slug <span style={{ color: '#64748b' }}>(used in /apps/{'{slug}'}/…)</span></div>
            <input
              value={renameSlug}
              onChange={e => { setRenameSlug(e.target.value.toLowerCase().replace(/[^a-z0-9-_]/g, '')); setSlugManual(true); }}
              onKeyDown={e => { if (e.key === 'Enter') commitRename(); if (e.key === 'Escape') setRenameApp(null); }}
              placeholder="url-slug"
              style={{ width: '100%', padding: '10px 14px', borderRadius: 8, border: `1px solid ${C.outlineVariant}`, background: C.surfaceContainer, color: '#a5b4fc', fontSize: 13, fontFamily: 'monospace', outline: 'none', boxSizing: 'border-box', marginBottom: 16 }}
            />
            <div style={{ display: 'flex', gap: 10, justifyContent: 'flex-end' }}>
              <button onClick={() => setRenameApp(null)} disabled={renaming} style={{ padding: '8px 18px', borderRadius: 8, border: `1px solid ${C.outlineVariant}`, background: 'none', color: C.textMuted, cursor: 'pointer', fontSize: 13 }}>Cancel</button>
              <button onClick={commitRename} disabled={renaming || !renameName.trim()} style={{ padding: '8px 18px', borderRadius: 8, border: 'none', background: '#6366f1', color: '#fff', cursor: renaming ? 'default' : 'pointer', fontSize: 13, fontWeight: 700, opacity: renaming ? 0.7 : 1 }}>
                {renaming ? 'Saving…' : 'Save'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* List-level toast */}
      {listToast && (
        <div style={{
          position: 'fixed', bottom: 32, right: 32, zIndex: 9999,
          background: listToast.ok ? C.greenBg : C.errorBg,
          border: `1px solid ${listToast.ok ? C.greenBorder : 'rgba(255,180,171,0.3)'}`,
          color: listToast.ok ? C.green : C.error,
          borderRadius: 10, padding: '10px 20px', fontSize: 13, fontWeight: 600,
        }}>
          {listToast.msg}
        </div>
      )}
    </div>
  );
}
