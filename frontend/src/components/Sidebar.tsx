'use client';
import Link from 'next/link';
import { usePathname, useRouter } from 'next/navigation';
import { useAuthStore } from '@/stores/authStore';
import { useEffect, useState } from 'react';

const NAV = [
  { href: '/dashboard', icon: 'dashboard', label: 'Command Center' },
  { href: '/runs',      icon: 'history',   label: 'Run History' },
];

const ADMIN_NAV = [
  { href: '/admin/orchestrators', icon: 'account_tree', label: 'Orchestrators' },
  { href: '/admin/agents',        icon: 'smart_toy',    label: 'Agents' },
  { href: '/admin/applications',  icon: 'apps',          label: 'Applications' },
  { href: '/admin/tokens',        icon: 'key',           label: 'Access Tokens' },
  { href: '/admin/playground',    icon: 'science',       label: 'Playground' },
  { href: '/admin/settings',      icon: 'settings',      label: 'Settings' },
];

export default function Sidebar() {
  const pathname = usePathname();
  const router = useRouter();
  const { user, logout } = useAuthStore();
  const [dark, setDark] = useState(false);

  // Sync state with the class set by the inline script in layout.tsx
  useEffect(() => {
    setDark(document.documentElement.classList.contains('dark'));
  }, []);

  function toggleTheme() {
    const next = !dark;
    setDark(next);
    document.documentElement.classList.toggle('dark', next);
    try { localStorage.setItem('tm-theme', next ? 'dark' : 'light'); } catch(e) {}
  }

  const isActive = (href: string) =>
    href === '/dashboard' ? pathname === href : pathname.startsWith(href);

  async function handleLogout() {
    await logout();
    router.replace('/login');
  }

  const initials = user?.name
    ? user.name.split(' ').map((n) => n[0]).join('').toUpperCase().slice(0, 2)
    : (user?.email?.[0] ?? 'O').toUpperCase();

  return (
    <>
      {/* Sidebar */}
      <aside style={{
        width: '260px', height: '100vh', position: 'fixed', left: 0, top: 0,
        background: 'var(--tm-sidebar)', borderRight: '1px solid rgba(255,255,255,.06)',
        display: 'flex', flexDirection: 'column', padding: '24px 0', zIndex: 40,
      }}>
        {/* Brand */}
        <div style={{ padding: '0 24px', marginBottom: '32px', display: 'flex', justifyContent: 'center' }}>
          <img src="/logos/theM-clean.svg" alt="the-M" style={{ height: '48px', width: 'auto', objectFit: 'contain', filter: 'drop-shadow(0 0 6px rgba(255,255,255,0.2))' }} />
        </div>

        {/* Nav */}
        <nav style={{ flex: 1, overflowY: 'auto', padding: '0 12px' }} className="custom-scrollbar">
          <p style={{ fontSize: '10px', fontWeight: 700, letterSpacing: '0.1em', color: 'rgba(255,255,255,.25)', textTransform: 'uppercase', padding: '0 12px', marginBottom: '8px' }}>
            Observe
          </p>
          {NAV.map(({ href, icon, label }) => (
            <Link key={href} href={href} style={{
              display: 'flex', alignItems: 'center', gap: '10px',
              padding: '8px 12px', borderRadius: '0 24px 24px 0',
              marginBottom: '2px', textDecoration: 'none', fontSize: '14px',
              transition: 'all .15s',
              background: isActive(href) ? 'var(--tm-accent-bg)' : 'transparent',
              color: isActive(href) ? 'var(--tm-accent)' : 'rgba(255,255,255,.45)',
              fontWeight: isActive(href) ? 600 : 400,
            }}
              onMouseEnter={(e) => { if (!isActive(href)) (e.currentTarget as HTMLElement).style.color = '#e8eaed'; }}
              onMouseLeave={(e) => { if (!isActive(href)) (e.currentTarget as HTMLElement).style.color = 'rgba(255,255,255,.45)'; }}
            >
              <span className="material-symbols-outlined" style={{ fontSize: '20px' }}>{icon}</span>
              {label}
            </Link>
          ))}

          {user?.role === 'admin' || user?.role === 'super_admin' ? (
            <>
              <p style={{ fontSize: '10px', fontWeight: 700, letterSpacing: '0.1em', color: 'rgba(255,255,255,.25)', textTransform: 'uppercase', padding: '16px 12px 8px' }}>
                Admin
              </p>
              {ADMIN_NAV.map(({ href, icon, label }) => (
                <Link key={href} href={href} style={{
                  display: 'flex', alignItems: 'center', gap: '10px',
                  padding: '8px 12px', borderRadius: '0 24px 24px 0',
                  marginBottom: '2px', textDecoration: 'none', fontSize: '14px',
                  transition: 'all .15s',
                  background: isActive(href) ? 'var(--tm-accent-bg)' : 'transparent',
                  color: isActive(href) ? 'var(--tm-accent)' : 'rgba(255,255,255,.45)',
                  fontWeight: isActive(href) ? 600 : 400,
                }}
                  onMouseEnter={(e) => { if (!isActive(href)) (e.currentTarget as HTMLElement).style.color = '#e8eaed'; }}
                  onMouseLeave={(e) => { if (!isActive(href)) (e.currentTarget as HTMLElement).style.color = 'rgba(255,255,255,.45)'; }}
                >
                  <span className="material-symbols-outlined" style={{ fontSize: '20px' }}>{icon}</span>
                  {label}
                </Link>
              ))}
            </>
          ) : null}
        </nav>

        {/* Footer: theme toggle + user */}
        <div style={{ padding: '16px 24px', borderTop: '1px solid rgba(255,255,255,.06)' }}>
          {/* Theme toggle */}
          <button
            onClick={toggleTheme}
            title={dark ? 'Switch to light mode' : 'Switch to dark mode'}
            style={{
              display: 'flex', alignItems: 'center', gap: '8px', width: '100%',
              background: 'rgba(255,255,255,.05)', border: '1px solid rgba(255,255,255,.08)',
              borderRadius: '8px', padding: '7px 10px', marginBottom: '12px',
              cursor: 'pointer', color: 'rgba(255,255,255,.5)', fontSize: '12px',
              transition: 'all .15s',
            }}
            onMouseEnter={(e) => { (e.currentTarget as HTMLElement).style.color = '#e8eaed'; (e.currentTarget as HTMLElement).style.background = 'rgba(255,255,255,.09)'; }}
            onMouseLeave={(e) => { (e.currentTarget as HTMLElement).style.color = 'rgba(255,255,255,.5)'; (e.currentTarget as HTMLElement).style.background = 'rgba(255,255,255,.05)'; }}
          >
            <span className="material-symbols-outlined" style={{ fontSize: '16px' }}>
              {dark ? 'light_mode' : 'dark_mode'}
            </span>
            {dark ? 'Light mode' : 'Dark mode'}
          </button>

          {/* User */}
          <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
            <div style={{
              width: '32px', height: '32px', borderRadius: '8px', flexShrink: 0,
              background: '#d7dbfd', color: '#585d7a',
              display: 'flex', alignItems: 'center', justifyContent: 'center',
              fontSize: '12px', fontWeight: 700,
            }}>{initials}</div>
            <div style={{ flex: 1, minWidth: 0 }}>
              <p style={{ fontSize: '13px', fontWeight: 600, color: '#e8eaed', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{user?.name || user?.email}</p>
              <p style={{ fontSize: '10px', color: 'rgba(255,255,255,.3)', textTransform: 'uppercase' }}>{user?.role}</p>
            </div>
            <button onClick={handleLogout} title="Sign out"
              style={{ color: 'rgba(255,255,255,.3)', background: 'none', border: 'none', cursor: 'pointer', padding: '4px' }}
              onMouseEnter={(e) => (e.currentTarget.style.color = '#ef4444')}
              onMouseLeave={(e) => (e.currentTarget.style.color = 'rgba(255,255,255,.3)')}>
              <span className="material-symbols-outlined" style={{ fontSize: '18px' }}>logout</span>
            </button>
          </div>
        </div>
      </aside>
    </>
  );
}
