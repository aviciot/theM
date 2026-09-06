import type { Metadata } from 'next';
import './globals.css';

export const metadata: Metadata = {
  title: 'the-M — Orchestration Platform',
  description: 'Multi-agent orchestration platform',
  icons: [{ rel: 'icon', url: '/favicon.svg', type: 'image/svg+xml' }],
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" suppressHydrationWarning>
      <head>
        {/* Apply saved theme before first paint to avoid flash */}
        <script dangerouslySetInnerHTML={{ __html: `
          try {
            if (localStorage.getItem('tm-theme') === 'dark') {
              document.documentElement.classList.add('dark');
            }
          } catch(e) {}
        `}} />
        {/* Fonts self-hosted — no external requests */}
      </head>
      <body>{children}</body>
    </html>
  );
}
