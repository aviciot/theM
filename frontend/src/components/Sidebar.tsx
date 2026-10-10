'use client';
import Link from 'next/link';
import { usePathname, useRouter } from 'next/navigation';
import { useAuthStore } from '@/stores/authStore';
import { themApi } from '@/lib/api';
import { useEffect, useState } from 'react';

const WORKSPACE_NAV = [
  { href: '/dashboard', icon: 'dashboard', label: 'Overview' },
  { href: '/runs',      icon: 'history',   label: 'Run History' },
];

const BUILD_TEST_NAV = [
  { href: '/admin/applications',  icon: 'apps',                label: 'Applications' },
  { href: '/admin/agents',        icon: 'smart_toy',           label: 'Agents' },
  { href: '/admin/gateway',       icon: 'hub',                 label: 'LLM Gateway' },
  { href: '/admin/mcp-servers',   icon: 'electrical_services', label: 'MCP Servers' },
  { href: '/admin/playground',    icon: 'science',             label: 'Playground' },
];

const MONITOR_NAV = [
  { href: '/admin/services',    icon: 'monitor_heart', label: 'Security Scans' },
  { href: '/admin/audit-logs',  icon: 'receipt_long',  label: 'Audit Logs' },
];

const ORGANIZATION_NAV = [
  { href: '/tenant/members',   icon: 'group',           label: 'Members' },
  { href: '/tenant/settings',  icon: 'manage_accounts', label: 'Organization Settings' },
  { href: '/admin/tokens',     icon: 'key',             label: 'Access Tokens' },
  { href: '/admin/settings',   icon: 'settings',        label: 'LLM & System Agents' },
];

const PLATFORM_ADMIN_NAV = [
  { href: '/admin/tenants',       icon: 'domain',          label: 'Tenants' },
  { href: '/admin/users',         icon: 'group',           label: 'Platform Users' },
  { href: '/admin/observability', icon: 'monitoring',      label: 'Usage & Quotas' },
  { href: '/admin/temporal',      icon: 'schedule_send',   label: 'Temporal' },
  { href: '/admin/settings',      icon: 'settings',        label: 'System Settings' },
];

function SectionLabel({ text }: { text: string }) {
  return (
    <p style={{ fontSize: '10px', fontWeight: 700, letterSpacing: '0.1em', color: 'rgba(255,255,255,.25)', textTransform: 'uppercase', padding: '16px 12px 8px', margin: 0 }}>
      {text}
    </p>
  );
}

function NavLink({ href, icon, label, active }: { href: string; icon: string; label: string; active: boolean }) {
  return (
    <Link href={href} style={{
      display: 'flex', alignItems: 'center', gap: '10px',
      padding: '8px 12px', borderRadius: '0 24px 24px 0',
      marginBottom: '2px', textDecoration: 'none', fontSize: '14px',
      transition: 'all .15s',
      background: active ? 'var(--tm-accent-bg)' : 'transparent',
      color: active ? 'var(--tm-accent)' : 'rgba(255,255,255,.45)',
      fontWeight: active ? 600 : 400,
    }}
      onMouseEnter={(e) => { if (!active) (e.currentTarget as HTMLElement).style.color = '#e8eaed'; }}
      onMouseLeave={(e) => { if (!active) (e.currentTarget as HTMLElement).style.color = 'rgba(255,255,255,.45)'; }}
    >
      <span className="material-symbols-outlined" style={{ fontSize: '20px' }}>{icon}</span>
      {label}
    </Link>
  );
}

export default function Sidebar() {
  const pathname = usePathname();
  const router = useRouter();
  const { user, logout } = useAuthStore();
  const [dark, setDark] = useState(false);
  const [tenantLogoUrl, setTenantLogoUrl] = useState<string | null>(null);

  useEffect(() => {
    if (!user || user.role === 'super_admin') return;
    themApi.getTenantSettings()
      .then(t => setTenantLogoUrl(t.logo_url ?? null))
      .catch(() => {});
  }, [user?.role]);

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
        <div style={{ padding: '0 24px', marginBottom: tenantLogoUrl ? '12px' : '32px', display: 'flex', justifyContent: 'center' }}>
          <a href="/" style={{ display: 'inline-flex', cursor: 'pointer' }}>
            <svg xmlns="http://www.w3.org/2000/svg" width="82" height="65" viewBox="0 0 1407 1118" style={{ filter: 'drop-shadow(0 0 6px rgba(160,240,208,0.3))' }}>
              <polygon points="88,77 184,146 244,191 281,217 336,259 355,272 358,272 367,267 372,266 379,262 391,258 433,239 440,237 473,222 513,206 520,202 546,192 555,187 558,187 446,102 433,91 421,83 403,68 397,65 392,60 331,15 318,4 274,19 264,21 246,28 239,29 217,37 214,37 211,39 201,41 189,46 186,46 154,57 151,57 148,59 141,60 138,62 104,73 101,73 98,75" fill="#a0f0d0" />
              <polygon points="1323,77 1313,75 1292,67 1289,67 1239,50 1236,50 1233,48 1230,48 1189,34 1176,31 1094,4 1085,12 1074,19 1053,36 959,106 855,187 876,196 881,197 973,237 980,239 1034,263 1048,268 1055,272 1059,272 1139,213 1146,209 1177,185 1188,178 1208,162 1284,107" fill="#a0f0d0" />
              <polygon points="70,97 70,334 71,335 72,350 76,365 104,429 108,435 180,486 184,490 245,534 345,609 339,293 305,269 281,250 252,230 182,177 179,176 153,156 150,155" fill="#a0f0d0" />
              <polygon points="1342,97 1252,162 1248,166 1152,236 1148,240 1126,255 1122,259 1112,265 1103,273 1074,293 1073,296 1073,317 1072,318 1072,355 1071,356 1071,415 1070,416 1070,461 1069,462 1069,526 1068,527 1067,609 1306,433 1325,392 1336,365 1341,343 1341,331 1342,330" fill="#a0f0d0" />
              <polygon points="682,361 576,210 381,292 532,410 577,395 580,395 586,392 595,390 613,384 616,382 622,381 664,367 667,365" fill="#a0f0d0" />
              <polygon points="732,361 803,384 806,386 809,386 831,394 834,394 860,404 863,404 881,410 1033,291 837,210 764,315 760,319 759,322 740,348" fill="#a0f0d0" />
              <polygon points="367,314 367,373 368,374 368,430 369,431 371,567 380,574 383,575 388,580 396,585 505,669 508,611 509,610 509,595 510,594 512,540 513,539 513,524 514,523 514,504 515,503 515,490 516,489 517,454 518,453 518,434 519,433 504,421 501,420 490,410 468,394 427,361 423,359 395,336 392,335" fill="#a0f0d0" />
              <polygon points="1046,314 894,433 895,456 896,457 896,475 897,476 897,494 898,495 898,513 899,514 901,561 902,562 902,579 903,580 903,594 904,595 906,650 907,651 907,666 908,669 934,648 971,621 1041,567" fill="#a0f0d0" />
              <polygon points="549,424 693,539 693,534 694,533 693,532 693,377 676,382 664,387 660,387 657,389 654,389 635,396 632,396" fill="#a0f0d0" />
              <polygon points="864,424 815,407 812,407 791,400 779,395 776,395 721,377 721,539 732,529 736,527 752,513" fill="#a0f0d0" />
              <polygon points="535,446 532,511 531,512 531,531 530,532 530,546 529,547 529,567 528,568 527,600 526,601 526,616 525,617 525,634 524,635 524,650 523,651 523,662 522,663 522,682 543,697 628,763 640,771 645,776 649,778 692,812 693,809 693,799 692,798 692,793 693,792 693,572 685,567 679,561 675,559 649,537 611,508 605,502 602,501" fill="#a0f0d0" />
              <polygon points="878,446 721,572 721,594 720,595 720,775 721,776 720,780 721,781 721,812 752,787 756,785 816,738 892,681 891,669 890,668 890,650 889,649 889,633 888,632 888,619 887,618 885,567 884,566 884,554 883,553 883,532 882,531 882,513 881,512" fill="#a0f0d0" />
              <polygon points="100,461 95,488 89,506 87,509 86,515 77,534 75,541 55,582 38,613 16,647 13,656 13,662 16,670 26,679 42,685 62,690 67,693 74,700 76,705 76,720 68,743 68,749 70,755 75,763 87,770 97,772 125,772 126,771 130,772 128,775 112,781 89,784 83,791 81,797 81,805 83,811 89,818 100,824 105,829 109,836 111,843 111,860 105,889 105,910 108,922 115,933 121,939 173,974 286,1057 326,1088 345,1105 345,641" fill="#a0f0d0" />
              <polygon points="1312,462 1273,489 1230,522 1227,523 1143,586 1067,641 1067,1106 1080,1093 1135,1050 1138,1049 1172,1023 1235,978 1239,974 1249,968 1253,964 1256,963 1270,952 1292,938 1301,928 1305,920 1307,912 1308,897 1307,896 1307,888 1301,858 1302,839 1307,829 1312,824 1323,818 1328,813 1331,806 1331,796 1330,792 1324,784 1311,783 1297,780 1286,776 1282,773 1284,771 1287,772 1316,772 1328,769 1335,765 1340,760 1344,750 1344,742 1336,717 1336,706 1339,699 1344,694 1353,689 1371,685 1386,679 1394,673 1399,663 1399,655 1397,649 1372,610 1339,546 1321,500 1321,497 1316,484" fill="#a0f0d0" />
            </svg>
          </a>
        </div>
        {tenantLogoUrl && (
          <div style={{ padding: '0 24px 20px', display: 'flex', justifyContent: 'flex-start' }}>
            <img src={tenantLogoUrl} alt="Tenant logo" style={{ maxHeight: '52px', maxWidth: '182px', objectFit: 'contain' }} />
          </div>
        )}

        {/* Nav */}
        <nav style={{ flex: 1, overflowY: 'auto', padding: '0 12px' }} className="custom-scrollbar">

          {/* ── Workspace (all users) ── */}
          <p style={{ fontSize: '10px', fontWeight: 700, letterSpacing: '0.1em', color: 'rgba(255,255,255,.25)', textTransform: 'uppercase', padding: '0 12px', marginBottom: '8px' }}>
            Workspace
          </p>
          {WORKSPACE_NAV.map(({ href, icon, label }) => (
            <NavLink key={href} href={href} icon={icon} label={label} active={isActive(href)} />
          ))}

          {/* ── Admin sections ── */}
          {(user?.role === 'admin' || user?.role === 'super_admin') && (
            <>
              <SectionLabel text="Build & Test" />
              {BUILD_TEST_NAV.map(({ href, icon, label }) => (
                <NavLink key={href} href={href} icon={icon} label={label} active={isActive(href)} />
              ))}

              <SectionLabel text="Monitor" />
              {MONITOR_NAV.map(({ href, icon, label }) => (
                <NavLink key={href} href={href} icon={icon} label={label} active={isActive(href)} />
              ))}

              <SectionLabel text="Organization" />
              {ORGANIZATION_NAV
                // /admin/settings already has its own entry under Platform Admin
                // for super_admin (labeled "System Settings") — don't show it twice.
                .filter(({ href }) => user?.role === 'super_admin' ? href !== '/admin/settings' : true)
                .map(({ href, icon, label }) => (
                  <NavLink key={href} href={href} icon={icon} label={label} active={isActive(href)} />
                ))}
            </>
          )}

          {/* ── Platform Admin (super_admin only) ── */}
          {user?.role === 'super_admin' && (
            <>
              <div style={{ margin: '12px 12px 0', borderTop: '1px solid rgba(255,255,255,.08)' }} />
              <SectionLabel text="Platform Admin" />
              {PLATFORM_ADMIN_NAV.map(({ href, icon, label }) => (
                <NavLink key={href} href={href} icon={icon} label={label} active={isActive(href)} />
              ))}
            </>
          )}

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
