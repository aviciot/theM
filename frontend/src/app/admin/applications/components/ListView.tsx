'use client';
import { useState, useEffect } from 'react';
import { themApi, type Application } from '@/lib/api';
import ChromaGrid from '@/components/ChromaGrid';
import { C, glass, APP_CARD_STYLES } from '../constants';
import { AppCard, useDashAppStatuses } from './AppCard';
import type { AppLiveness } from '../types';

export function ListView({
  list, loading, onNew, onEdit, onSessions, onRuntime, onMCPCredentials, onToggle, onDelete, onReload,
  selectedApps, onToggleSelect, onSelectAll, onBulkDelete, bulkDeleting,
}: {
  list: Application[];
  loading: boolean;
  onNew: () => void;
  onEdit: (app: Application) => void;
  onSessions: (app: Application) => void;
  onRuntime: (app: Application) => void;
  onMCPCredentials: (app: Application) => void;
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
            onSessions={onSessions}
            onRuntime={onRuntime}
            onMCPCredentials={onMCPCredentials}
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
