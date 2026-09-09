'use client';
import Link from 'next/link';
import { usePathname } from 'next/navigation';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import type { Application } from '@/lib/api';
import { C } from '../constants';

const TABS = [
  { label: 'Builder',          href: (id: string) => `/admin/applications/${id}/builder` },
  { label: 'Runtime',          href: (id: string) => `/admin/applications/${id}/runtime` },
  { label: 'MCP Credentials',  href: (id: string) => `/admin/applications/${id}/mcp-credentials` },
  { label: 'Monitor',          href: (id: string) => `/admin/applications/${id}/monitor` },
];

export function AppDetailShell({
  app,
  children,
}: {
  app: Application;
  children: React.ReactNode;
}) {
  const pathname = usePathname();

  return (
    <AuthGuard>
      <div style={{ display: 'flex', minHeight: '100vh', background: C.bg }}>
        <Sidebar />
        <div style={{ marginLeft: 260, flex: 1, display: 'flex', flexDirection: 'column', minHeight: '100vh', overflow: 'hidden' }}>

          {/* Breadcrumb + tab bar */}
          <div style={{ flexShrink: 0, borderBottom: `1px solid ${C.outline}`, background: C.bg }}>
            {/* Breadcrumb */}
            <div style={{ padding: '14px 28px 0', display: 'flex', alignItems: 'center', gap: '6px', fontSize: '13px', color: C.textMuted }}>
              <Link href="/admin/applications" style={{ color: C.textMuted, textDecoration: 'none', display: 'flex', alignItems: 'center', gap: '4px' }}
                onMouseEnter={e => (e.currentTarget.style.color = C.text)}
                onMouseLeave={e => (e.currentTarget.style.color = C.textMuted)}
              >
                <span className="material-symbols-outlined" style={{ fontSize: '15px' }}>apps</span>
                Applications
              </Link>
              <span className="material-symbols-outlined" style={{ fontSize: '14px' }}>chevron_right</span>
              <span style={{ color: C.text, fontWeight: 600, maxWidth: 320, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                {app.name}
              </span>
              {!app.enabled && (
                <span style={{ marginLeft: 6, fontSize: '10px', fontWeight: 700, padding: '1px 7px', borderRadius: '9999px', background: 'rgba(100,116,139,0.15)', color: '#64748b', border: '1px solid rgba(100,116,139,0.25)', textTransform: 'uppercase', letterSpacing: '0.06em' }}>
                  disabled
                </span>
              )}
            </div>

            {/* Tabs */}
            <div style={{ display: 'flex', gap: '0', padding: '0 20px', marginTop: '8px' }}>
              {TABS.map(({ label, href }) => {
                const url = href(app.id);
                const active = pathname === url || pathname.startsWith(url + '/');
                return (
                  <Link
                    key={url}
                    href={url}
                    style={{
                      padding: '8px 16px',
                      fontSize: '13px',
                      fontWeight: active ? 600 : 400,
                      color: active ? '#00d1ff' : C.textMuted,
                      textDecoration: 'none',
                      borderBottom: active ? '2px solid #00d1ff' : '2px solid transparent',
                      transition: 'color 150ms ease, border-color 150ms ease',
                      whiteSpace: 'nowrap',
                    }}
                    onMouseEnter={e => { if (!active) e.currentTarget.style.color = C.text; }}
                    onMouseLeave={e => { if (!active) e.currentTarget.style.color = C.textMuted; }}
                  >
                    {label}
                  </Link>
                );
              })}
            </div>
          </div>

          {/* Tab content */}
          <div style={{ flex: 1, overflow: 'hidden', display: 'flex', flexDirection: 'column' }}>
            {children}
          </div>

        </div>
      </div>
    </AuthGuard>
  );
}
