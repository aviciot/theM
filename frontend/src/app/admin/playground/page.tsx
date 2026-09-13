'use client';
import { useEffect, useState, useCallback, useMemo, Suspense } from 'react';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { themApi, type Application } from '@/lib/api';
import { type ConnTarget, targetId, targetLabel, TAB_COLORS } from './playgroundTypes';
import { ChatColumn } from './ChatColumn';

// ── EPCredentialSelector ──────────────────────────────────────────────────────
// Shows EP access mode and provides a single "Connect with token" button for
// token-mode EPs. The token's is_backend flag is set automatically based on
// the EP's allowed_principals:
//   external → is_backend=true (Service token)
//   internal → is_backend=false (Internal token)
//   both / unset → is_backend=true (defaults to Service token)
// Tokens expire after 20 minutes.

interface EPCredentialSelectorProps {
  target: ConnTarget | null;
  applications: Application[];
  overrideToken: string | null;
  onToken: (token: string | null) => void;
}

function EPCredentialSelector({ target, applications, overrideToken, onToken }: EPCredentialSelectorProps) {
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState<string | null>(null);

  if (!target || target.kind !== 'entrypoint') return null;

  const app = applications.find(a => (a.slug ?? a.id) === target.appSlug);
  const ep = app?.entry_points.find(e => e.slug === target.slug);
  const mode = (ep?.access_policy as Record<string, string> | undefined)?.mode ?? 'token';
  const allowedPrincipals = ep?.allowed_principals ?? 'both';

  // Non-token modes: just show a label, no token needed
  if (mode === 'public') {
    return <span style={{ fontSize: 11, color: 'var(--tm-text-muted)', flexShrink: 0 }}>Access: public</span>;
  }
  if (mode === 'user_jwt') {
    return <span style={{ fontSize: 11, color: 'var(--tm-text-muted)', flexShrink: 0 }}>Access: staff login (JWT)</span>;
  }
  if (mode === 'external_jwt') {
    return <span style={{ fontSize: 11, color: 'var(--tm-text-muted)', flexShrink: 0 }}>Access: external JWT</span>;
  }

  // token mode — single button
  const isBackend = allowedPrincipals !== 'internal';
  const tokenLabel = isBackend ? 'Service token' : 'Internal token';

  const handleConnect = async () => {
    if (overrideToken) { onToken(null); return; }
    setCreating(true);
    setError(null);
    try {
      const expiresAt = new Date(Date.now() + 20 * 60 * 1000).toISOString();
      const result = await themApi.createToken({
        label: `playground-${target.slug}`,
        user_id: 0,
        is_backend: isBackend,
        expires_at: expiresAt,
      });
      if (!result.token) throw new Error('Token value not returned');
      onToken(result.token);
    } catch (e) {
      setError((e as Error).message || 'Failed to create token');
    } finally {
      setCreating(false);
    }
  };

  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 6, flexShrink: 0 }}>
      <span style={{ fontSize: 11, color: 'var(--tm-text-muted)', flexShrink: 0 }}>
        Access: {allowedPrincipals === 'internal' ? 'internal only' : allowedPrincipals === 'external' ? 'external only' : 'token'}
      </span>
      <button
        onClick={handleConnect}
        disabled={creating}
        style={{
          padding: '3px 8px', borderRadius: 6, border: '1px solid',
          borderColor: overrideToken ? '#10b981' : 'var(--tm-border)',
          background: overrideToken ? 'rgba(16,185,129,0.12)' : 'transparent',
          color: overrideToken ? '#4edea3' : 'var(--tm-text-muted)',
          fontSize: 11, fontWeight: 600, cursor: creating ? 'wait' : 'pointer',
        }}
        title={overrideToken ? `Using ${tokenLabel} — click to disconnect` : `Create a 20-min ${tokenLabel} for this EP`}
      >
        {creating ? 'Creating…' : overrideToken ? `${tokenLabel} (active)` : `Connect with token`}
      </button>
      {error && <span style={{ fontSize: 11, color: '#f87171' }}>{error}</span>}
      {overrideToken && !error && (
        <span style={{ fontSize: 11, color: '#4edea3' }}>Token active — send a message to connect</span>
      )}
    </div>
  );
}

// ── TargetSelector ──────────────────────────────────────────────────────────
// Grouped optgroup dropdown: Orchestrators (direct) + per-app EP groups.

interface TargetSelectorProps {
  applications: Application[];
  value: ConnTarget | null;
  onChange: (t: ConnTarget) => void;
}

function TargetSelector({ applications, value, onChange }: TargetSelectorProps) {
  const encodeTarget = (t: ConnTarget) => targetId(t);

  const decodeTarget = useCallback((v: string): ConnTarget | null => {
    if (v.startsWith('ep:')) {
      const rest = v.slice(3);
      const sep = rest.indexOf('/');
      const encodedAppSlug = sep >= 0 ? rest.slice(0, sep) : '';
      const slug = sep >= 0 ? rest.slice(sep + 1) : rest;
      for (const app of applications) {
        if (encodedAppSlug && app.slug !== encodedAppSlug && app.id !== encodedAppSlug) continue;
        const ep = app.entry_points.find(e => e.slug === slug);
        if (ep && (ep.entry_point_type === 'websocket' || ep.entry_point_type === 'sse' || ep.entry_point_type === 'voice' || ep.entry_point_type === 'a2a')) {
          const resolvedAppSlug = app.slug ?? app.id;
          return { kind: 'entrypoint', slug, appSlug: resolvedAppSlug, tenantSlug: ep.tenant_slug ?? app.tenant_slug ?? 'default', epType: ep.entry_point_type as 'websocket' | 'sse' | 'voice' | 'a2a', appName: app.name, orchName: app.app_orchestrators?.[0]?.name ?? '' };
        }
      }
    }
    return null;
  }, [applications]);

  const selectedValue = value ? encodeTarget(value) : '';

  return (
    <select
      value={selectedValue}
      onChange={e => { const t = decodeTarget(e.target.value); if (t) onChange(t); }}
      style={{ padding: '6px 10px', borderRadius: 8, border: '1px solid var(--tm-border)', background: 'var(--tm-surface)', color: 'var(--tm-text)', fontSize: 13, cursor: 'pointer', maxWidth: 260 }}
    >
      {applications.filter(a => a.enabled && a.entry_points.some(e => e.enabled && ['websocket', 'sse', 'voice', 'a2a'].includes(e.entry_point_type))).map(app => (
        <optgroup key={app.id} label={`App: ${app.name}`}>
          {app.entry_points.filter(e => e.enabled && ['websocket', 'sse', 'voice', 'a2a'].includes(e.entry_point_type)).map(ep => (
            <option key={ep.id} value={`ep:${app.slug ?? app.id}/${ep.slug}`}>
              {ep.slug} [{ep.entry_point_type}]
            </option>
          ))}
        </optgroup>
      ))}
    </select>
  );
}

// ── PlaygroundInner ─────────────────────────────────────────────────────────

function PlaygroundInner() {
  const [applications, setApplications] = useState<Application[]>([]);
  const [tabs, setTabs] = useState<ConnTarget[]>([]);
  const [activeTabId, setActiveTabId] = useState<string>('');
  const [compareMode, setCompareMode] = useState(false);
  // overrideTokens: keyed by tab id — holds the plain service token value for
  // tabs where the user chose "Service token" mode. Cleared on tab close.
  const [overrideTokens, setOverrideTokens] = useState<Record<string, string>>({});
  const [composeInput, setComposeInput] = useState('');
  const [broadcastText, setBroadcastText] = useState<string | null>(null);
  const sentCount = { current: 0 };
  const [webrtcSlugs, setWebrtcSlugs] = useState<Record<string, { appSlug: string; epSlug: string; tenantSlug: string }>>({});

  const applyApps = useCallback((apps: Application[]) => {
    setApplications(apps);
    for (const a of apps) {
      if (!a.enabled) continue;
      const ep = a.entry_points.find(e => e.enabled && ['websocket', 'sse', 'voice', 'a2a'].includes(e.entry_point_type));
      if (ep) {
        const t: ConnTarget = { kind: 'entrypoint', slug: ep.slug, appSlug: a.slug ?? a.id, tenantSlug: ep.tenant_slug ?? a.tenant_slug ?? 'default', epType: ep.entry_point_type as 'websocket' | 'sse' | 'voice' | 'a2a', appName: a.name, orchName: a.app_orchestrators?.[0]?.name ?? '' };
        setTabs([t]);
        setActiveTabId(targetId(t));
        break;
      }
    }
    const m: Record<string, { appSlug: string; epSlug: string; tenantSlug: string }> = {};
    for (const a of apps) {
      if (!a.enabled) continue;
      const ep = a.entry_points.find(e => e.enabled && e.entry_point_type === 'webrtc');
      const aoName = a.app_orchestrators?.[0]?.name;
      if (ep && aoName && !m[aoName]) m[aoName] = { appSlug: a.slug ?? a.id, epSlug: ep.slug, tenantSlug: ep.tenant_slug ?? a.tenant_slug ?? 'default' };
    }
    setWebrtcSlugs(m);
  }, []);

  const loadApps = useCallback(() => {
    themApi.applications().then(applyApps).catch(() => {});
  }, [applyApps]);

  useEffect(() => {
    let cancelled = false;
    themApi.applications()
      .then(apps => { if (!cancelled) applyApps(apps); })
      .catch(() => {});
    const onFocus = () => loadApps();
    const onVisible = () => { if (document.visibilityState === 'visible') loadApps(); };
    window.addEventListener('focus', onFocus);
    document.addEventListener('visibilitychange', onVisible);
    return () => {
      cancelled = true;
      window.removeEventListener('focus', onFocus);
      document.removeEventListener('visibilitychange', onVisible);
    };
  }, [applyApps, loadApps]);

  const activeTab = useMemo(() => tabs.find(t => targetId(t) === activeTabId) ?? null, [tabs, activeTabId]);

  const openNewTab = (t: ConnTarget) => {
    const id = targetId(t);
    if (!tabs.some(x => targetId(x) === id)) {
      setTabs(prev => [...prev, t]);
    }
    setActiveTabId(id);
    setCompareMode(false);
  };

  const closeTab = (id: string) => {
    setTabs(prev => {
      const next = prev.filter(t => targetId(t) !== id);
      if (activeTabId === id && next.length > 0) setActiveTabId(targetId(next[next.length - 1]));
      return next;
    });
    setOverrideTokens(prev => { const next = { ...prev }; delete next[id]; return next; });
    if (compareMode && tabs.length <= 2) setCompareMode(false);
  };

  const activeWebrtc = useMemo(() => {
    if (!activeTab) return null;
    const name = activeTab.kind === 'orchestrator' ? activeTab.name : activeTab.orchName;
    return webrtcSlugs[name] ?? null;
  }, [activeTab, webrtcSlugs]);

  const handleBroadcast = () => {
    if (!composeInput.trim()) return;
    sentCount.current = 0;
    setBroadcastText(composeInput);
    setComposeInput('');
  };

  const onBroadcastSent = useCallback(() => {
    sentCount.current += 1;
    if (sentCount.current >= tabs.length) setBroadcastText(null);
  }, [tabs.length]);

  const onComposeKey = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); handleBroadcast(); }
  };

  return (
    <AuthGuard>
      <div style={{ display: 'flex', height: '100vh', background: 'var(--tm-bg)' }}>
        <Sidebar />
        <div style={{ marginLeft: 260, flex: 1, display: 'flex', flexDirection: 'column', overflow: 'hidden', minWidth: 0 }}>

          {/* ── Header ── */}
          <div style={{ padding: '12px 20px', borderBottom: '1px solid var(--tm-border)', display: 'flex', alignItems: 'center', gap: 12, flexShrink: 0 }}>
            <div style={{ fontSize: 17, fontWeight: 700, color: 'var(--tm-text)' }}>Playground</div>

            <TargetSelector
              applications={applications}
              value={activeTab}
              onChange={t => openNewTab(t)}
            />

            <EPCredentialSelector
              target={activeTab}
              applications={applications}
              overrideToken={activeTabId ? (overrideTokens[activeTabId] ?? null) : null}
              onToken={tok => setOverrideTokens(prev => {
                if (!activeTabId) return prev;
                if (tok === null) { const next = { ...prev }; delete next[activeTabId]; return next; }
                return { ...prev, [activeTabId]: tok };
              })}
            />

            {/* Tab bar */}
            <div style={{ display: 'flex', gap: 4, alignItems: 'center', overflow: 'hidden', flex: 1, minWidth: 0 }}>
              {tabs.map((t, idx) => {
                const id = targetId(t);
                const isActive = id === activeTabId;
                const color = TAB_COLORS[idx % TAB_COLORS.length];
                return (
                  <div
                    key={id}
                    onClick={() => { setActiveTabId(id); setCompareMode(false); }}
                    style={{
                      display: 'flex', alignItems: 'center', gap: 5,
                      padding: '4px 10px', borderRadius: 8, cursor: 'pointer', flexShrink: 0,
                      background: isActive ? `${color}22` : 'var(--tm-surface)',
                      border: `1.5px solid ${isActive ? color : 'var(--tm-border)'}`,
                      maxWidth: 180, overflow: 'hidden',
                    }}
                  >
                    <span style={{ width: 7, height: 7, borderRadius: '50%', background: color, flexShrink: 0 }} />
                    <span style={{ fontSize: 12, fontWeight: isActive ? 600 : 400, color: isActive ? 'var(--tm-text)' : 'var(--tm-text-muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {targetLabel(t)}
                    </span>
                    {t.kind === 'entrypoint' && (
                      <span style={{ fontSize: 9, padding: '0px 4px', borderRadius: 3, background: 'rgba(124,58,237,0.2)', color: '#a78bfa', fontWeight: 700, flexShrink: 0 }}>
                        {t.epType === 'websocket' ? 'WS' : t.epType === 'sse' ? 'SSE' : t.epType === 'a2a' ? 'A2A' : 'VOI'}
                      </span>
                    )}
                    {tabs.length > 1 && (
                      <span
                        onClick={e => { e.stopPropagation(); closeTab(id); }}
                        style={{ fontSize: 13, color: 'var(--tm-text-muted)', cursor: 'pointer', lineHeight: 1, paddingLeft: 2, flexShrink: 0 }}
                        title="Close tab"
                      >×</span>
                    )}
                  </div>
                );
              })}
            </div>

            {tabs.length >= 2 && (
              <button
                onClick={() => setCompareMode(m => !m)}
                style={{ padding: '5px 12px', borderRadius: 8, border: `1.5px solid ${compareMode ? '#7c3aed' : 'var(--tm-border)'}`, background: compareMode ? 'rgba(124,58,237,0.12)' : 'var(--tm-surface)', color: compareMode ? '#a78bfa' : 'var(--tm-text-muted)', fontSize: 12, fontWeight: 600, cursor: 'pointer', flexShrink: 0 }}
              >
                {compareMode ? '⊞ Comparing' : '⊞ Compare'}
              </button>
            )}

            <button
              onClick={() => activeWebrtc && window.open(`/${activeWebrtc.tenantSlug ?? 'default'}/apps/${activeWebrtc.appSlug}/${activeWebrtc.epSlug}/voice`, '_blank', 'noopener')}
              disabled={!activeWebrtc}
              title={activeWebrtc ? `Open voice room (${activeWebrtc.appSlug}/${activeWebrtc.epSlug})` : 'No WebRTC app configured for this target'}
              style={{ width: 34, height: 34, borderRadius: 9, border: '1.5px solid', borderColor: activeWebrtc ? 'rgba(99,202,183,0.6)' : 'var(--tm-border)', background: activeWebrtc ? 'rgba(99,202,183,0.08)' : 'var(--tm-surface)', cursor: activeWebrtc ? 'pointer' : 'not-allowed', display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0, opacity: activeWebrtc ? 1 : 0.35 }}
            >
              <svg width="18" height="18" viewBox="0 0 100 100" fill="none">
                <circle cx="50" cy="28" r="22" fill={activeWebrtc ? '#63cab7' : 'currentColor'} opacity="0.9"/>
                <circle cx="30" cy="65" r="22" fill={activeWebrtc ? '#f08030' : 'currentColor'} opacity="0.9"/>
                <circle cx="70" cy="65" r="22" fill={activeWebrtc ? '#63cab7' : 'currentColor'} opacity="0.7"/>
                <circle cx="50" cy="28" r="22" fill="none" stroke="var(--tm-bg)" strokeWidth="3"/>
                <circle cx="30" cy="65" r="22" fill="none" stroke="var(--tm-bg)" strokeWidth="3"/>
                <circle cx="70" cy="65" r="22" fill="none" stroke="var(--tm-bg)" strokeWidth="3"/>
              </svg>
            </button>
          </div>

          {/* ── Content area ── */}
          {tabs.length === 0 ? (
            <div style={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center', color: 'var(--tm-text-muted)', fontSize: 14 }}>
              Select a target above to open a chat tab
            </div>
          ) : compareMode ? (
            <div style={{ flex: 1, display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
              <div style={{ flex: 1, display: 'flex', overflow: 'hidden' }}>
                {tabs.map((t, idx) => {
                  const tabId = targetId(t);
                  const app = t.kind === 'entrypoint' ? applications.find(a => (a.slug ?? a.id) === t.appSlug) : null;
                  const ep = app?.entry_points.find(e => e.slug === (t as {slug:string}).slug);
                  const principals = ep?.allowed_principals ?? 'both';
                  const isBackend = principals !== 'internal';
                  return (
                    <ChatColumn
                      key={tabId}
                      target={t}
                      color={TAB_COLORS[idx % TAB_COLORS.length]}
                      sharedInput={broadcastText}
                      onSharedSent={onBroadcastSent}
                      showHeader
                      compact={tabs.length >= 3}
                      overrideToken={overrideTokens[tabId]}
                      tokenIsBackend={isBackend}
                      onToken={tok => setOverrideTokens(prev => {
                        if (tok === null) { const next = { ...prev }; delete next[tabId]; return next; }
                        return { ...prev, [tabId]: tok };
                      })}
                    />
                  );
                })}
              </div>
              <div style={{ padding: '10px 16px', borderTop: '2px solid var(--tm-border)', display: 'flex', gap: 8, background: 'var(--tm-surface)', alignItems: 'flex-end', flexShrink: 0 }}>
                <div style={{ fontSize: 11, color: 'var(--tm-text-muted)', alignSelf: 'center', flexShrink: 0 }}>
                  Broadcast to {tabs.length} columns
                </div>
                <textarea value={composeInput} onChange={e => setComposeInput(e.target.value)} onKeyDown={onComposeKey} dir="auto"
                  placeholder="Type a message — sends to all columns simultaneously (Enter to send)"
                  rows={2}
                  style={{ flex: 1, padding: '9px 12px', borderRadius: 10, border: '1px solid var(--tm-border)', background: 'var(--tm-bg)', color: 'var(--tm-text)', fontSize: 13, resize: 'none', outline: 'none', fontFamily: 'inherit', lineHeight: 1.5 }}
                />
                <button onClick={handleBroadcast} disabled={!composeInput.trim()}
                  style={{ padding: '9px 18px', borderRadius: 10, border: 'none', background: !composeInput.trim() ? 'var(--tm-surface)' : '#7c3aed', color: !composeInput.trim() ? 'var(--tm-text-muted)' : '#fff', fontSize: 13, fontWeight: 600, cursor: !composeInput.trim() ? 'not-allowed' : 'pointer' }}>
                  Send all
                </button>
              </div>
            </div>
          ) : (
            activeTab && (
              <ChatColumn
                key={targetId(activeTab)}
                target={activeTab}
                color={TAB_COLORS[tabs.indexOf(activeTab) % TAB_COLORS.length]}
                showHeader
                overrideToken={overrideTokens[activeTabId]}
              />
            )
          )}
        </div>
      </div>
    </AuthGuard>
  );
}

export default function PlaygroundPage() {
  return (
    <Suspense>
      <PlaygroundInner />
    </Suspense>
  );
}
