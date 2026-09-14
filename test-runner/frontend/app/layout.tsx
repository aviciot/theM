import type { Metadata } from 'next';
import './globals.css';
import Link from 'next/link';

export const metadata: Metadata = {
  title: 'the-M Test Runner',
  description: 'End-user scenario testing for the-M',
};

const nav = [
  { href: '/config', label: 'Config', icon: '⚙️' },
  { href: '/scenarios', label: 'Scenarios', icon: '📋' },
  { href: '/history', label: 'History', icon: '🕐' },
];

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body className="bg-gray-950 text-gray-100 min-h-screen flex">
        <aside className="w-56 bg-gray-900 border-r border-gray-800 flex flex-col shrink-0">
          <div className="px-5 py-5 border-b border-gray-800">
            <div className="text-sm font-bold text-indigo-400 tracking-widest uppercase">the-M</div>
            <div className="text-xs text-gray-500 mt-0.5">Test Runner</div>
          </div>
          <nav className="flex-1 py-4 flex flex-col gap-1 px-2">
            {nav.map((n) => (
              <Link
                key={n.href}
                href={n.href}
                className="flex items-center gap-3 px-3 py-2 rounded-lg text-sm text-gray-300 hover:bg-gray-800 hover:text-white transition-colors"
              >
                <span>{n.icon}</span>
                {n.label}
              </Link>
            ))}
          </nav>
          <div className="px-5 py-4 border-t border-gray-800 text-xs text-gray-600">
            v0.1.0
          </div>
        </aside>
        <main className="flex-1 overflow-auto">{children}</main>
      </body>
    </html>
  );
}
